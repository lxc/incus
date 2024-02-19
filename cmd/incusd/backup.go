package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/internal/instancewriter"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/backup"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/internal/server/task"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/idmap"
	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
)

// Create a new backup.
func backupCreate(s *state.State, args db.InstanceBackup, sourceInst instance.Instance, op *operations.Operation) error {
	l := logger.AddContext(logger.Ctx{"project": sourceInst.Project().Name, "instance": sourceInst.Name(), "name": args.Name})
	l.Debug("Instance backup started")
	defer l.Debug("Instance backup finished")

	revert := revert.New()
	defer revert.Fail()

	// Get storage pool.
	pool, err := storagePools.LoadByInstance(s, sourceInst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	// Ignore requests for optimized backups when pool driver doesn't support it.
	if args.OptimizedStorage && !pool.Driver().Info().OptimizedBackups {
		args.OptimizedStorage = false
	}

	// Create the database entry.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.CreateInstanceBackup(ctx, args)
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("Backup %q already exists", args.Name)
		}

		return fmt.Errorf("Insert backup info into database: %w", err)
	}

	revert.Add(func() {
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.DeleteInstanceBackup(ctx, args.Name)
		})
	})

	// Get the backup struct.
	b, err := instance.BackupLoadByName(s, sourceInst.Project().Name, args.Name)
	if err != nil {
		return fmt.Errorf("Load backup object: %w", err)
	}

	// Detect compression method.
	var compress string
	b.SetCompressionAlgorithm(args.CompressionAlgorithm)
	if b.CompressionAlgorithm() != "" {
		compress = b.CompressionAlgorithm()
	} else {
		var p *api.Project
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			project, err := dbCluster.GetProject(ctx, tx.Tx(), sourceInst.Project().Name)
			if err != nil {
				return err
			}

			p, err = project.ToAPI(ctx, tx.Tx())

			return err
		})
		if err != nil {
			return err
		}

		if p.Config["backups.compression_algorithm"] != "" {
			compress = p.Config["backups.compression_algorithm"]
		} else {
			compress = s.GlobalConfig.BackupsCompressionAlgorithm()
		}
	}

	// Create the target path if needed.
	backupsPath := internalUtil.VarPath("backups", "instances", project.Instance(sourceInst.Project().Name, sourceInst.Name()))
	if !util.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = os.Remove(backupsPath) })
	}

	target := internalUtil.VarPath("backups", "instances", project.Instance(sourceInst.Project().Name, b.Name()))

	// Setup the tarball writer.
	l.Debug("Opening backup tarball for writing", logger.Ctx{"path": target})
	tarFileWriter, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("Error opening backup tarball for writing %q: %w", target, err)
	}

	defer func() { _ = tarFileWriter.Close() }()
	revert.Add(func() { _ = os.Remove(target) })

	// Get IDMap to unshift container as the tarball is created.
	var idmapSet *idmap.Set
	if sourceInst.Type() == instancetype.Container {
		c := sourceInst.(instance.Container)
		idmapSet, err = c.DiskIdmap()
		if err != nil {
			return fmt.Errorf("Error getting container IDMAP: %w", err)
		}
	}

	// Create the tarball.
	tarPipeReader, tarPipeWriter := io.Pipe()
	defer func() { _ = tarPipeWriter.Close() }() // Ensure that go routine below always ends.
	tarWriter := instancewriter.NewInstanceTarWriter(tarPipeWriter, idmapSet)

	// Setup tar writer go routine, with optional compression.
	tarWriterRes := make(chan error)
	var compressErr error

	backupProgressWriter := &ioprogress.ProgressWriter{
		Tracker: &ioprogress.ProgressTracker{
			Handler: func(value, speed int64) {
				meta := op.Metadata()
				if meta == nil {
					meta = make(map[string]any)
				}

				progressText := fmt.Sprintf("%s (%s/s)", units.GetByteSizeString(value, 2), units.GetByteSizeString(speed, 2))
				meta["create_backup_progress"] = progressText
				_ = op.UpdateMetadata(meta)
			},
		},
	}

	go func(resCh chan<- error) {
		l.Debug("Started backup tarball writer")
		defer l.Debug("Finished backup tarball writer")
		if compress != "none" {
			backupProgressWriter.WriteCloser = tarFileWriter
			compressErr = compressFile(compress, tarPipeReader, backupProgressWriter)

			// If a compression error occurred, close the tarPipeWriter to end the export.
			if compressErr != nil {
				_ = tarPipeWriter.Close()
			}
		} else {
			backupProgressWriter.WriteCloser = tarFileWriter
			_, err = io.Copy(backupProgressWriter, tarPipeReader)
		}

		resCh <- err
	}(tarWriterRes)

	// Write index file.
	l.Debug("Adding backup index file")
	err = backupWriteIndex(sourceInst, pool, b.OptimizedStorage(), !b.InstanceOnly(), tarWriter)

	// Check compression errors.
	if compressErr != nil {
		return compressErr
	}

	// Check backupWriteIndex for errors.
	if err != nil {
		return fmt.Errorf("Error writing backup index file: %w", err)
	}

	err = pool.BackupInstance(sourceInst, tarWriter, b.OptimizedStorage(), !b.InstanceOnly(), nil)
	if err != nil {
		return fmt.Errorf("Backup create: %w", err)
	}

	// Close off the tarball file.
	err = tarWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball writer: %w", err)
	}

	// Close off the tarball pipe writer (this will end the go routine above).
	err = tarPipeWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball pipe writer: %w", err)
	}

	err = <-tarWriterRes
	if err != nil {
		return fmt.Errorf("Error writing tarball: %w", err)
	}

	err = tarFileWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tar file: %w", err)
	}

	revert.Success()
	s.Events.SendLifecycle(sourceInst.Project().Name, lifecycle.InstanceBackupCreated.Event(args.Name, b.Instance(), nil))

	return nil
}

// backupWriteIndex generates an index.yaml file and then writes it to the root of the backup tarball.
func backupWriteIndex(sourceInst instance.Instance, pool storagePools.Pool, optimized bool, snapshots bool, tarWriter *instancewriter.InstanceTarWriter) error {
	// Indicate whether the driver will include a driver-specific optimized header.
	poolDriverOptimizedHeader := false
	if optimized {
		poolDriverOptimizedHeader = pool.Driver().Info().OptimizedBackupHeader
	}

	backupType := backup.InstanceTypeToBackupType(api.InstanceType(sourceInst.Type().String()))
	if backupType == backup.TypeUnknown {
		return fmt.Errorf("Unrecognised instance type for backup type conversion")
	}

	// We only write backup files out for actual instances.
	if sourceInst.IsSnapshot() {
		return fmt.Errorf("Cannot generate backup config for snapshots")
	}

	// Immediately return if the instance directory doesn't exist yet.
	if !util.PathExists(sourceInst.Path()) {
		return os.ErrNotExist
	}

	config, err := pool.GenerateInstanceBackupConfig(sourceInst, snapshots, nil)
	if err != nil {
		return fmt.Errorf("Failed generating instance backup config: %w", err)
	}

	indexInfo := backup.Info{
		Name:             sourceInst.Name(),
		Pool:             pool.Name(),
		Backend:          pool.Driver().Info().Name,
		Type:             backupType,
		OptimizedStorage: &optimized,
		OptimizedHeader:  &poolDriverOptimizedHeader,
		Config:           config,
	}

	if snapshots {
		indexInfo.Snapshots = make([]string, 0, len(config.Snapshots))
		for _, s := range config.Snapshots {
			indexInfo.Snapshots = append(indexInfo.Snapshots, s.Name)
		}
	}

	// Convert to YAML.
	indexData, err := yaml.Marshal(&indexInfo)
	if err != nil {
		return err
	}

	r := bytes.NewReader(indexData)

	indexFileInfo := instancewriter.FileInfo{
		FileName:    "backup/index.yaml",
		FileSize:    int64(len(indexData)),
		FileMode:    0644,
		FileModTime: time.Now(),
	}

	// Write to tarball.
	err = tarWriter.WriteFileFromReader(r, &indexFileInfo)
	if err != nil {
		return err
	}

	return nil
}

func pruneExpiredBackupsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		opRun := func(op *operations.Operation) error {
			err := pruneExpiredInstanceBackups(ctx, s)
			if err != nil {
				return fmt.Errorf("Failed pruning expired instance backups: %w", err)
			}

			err = pruneExpiredStorageVolumeBackups(ctx, s)
			if err != nil {
				return fmt.Errorf("Failed pruning expired storage volume backups: %w", err)
			}

			err = pruneExpiredStorageBucketBackups(ctx, s)
			if err != nil {
				return fmt.Errorf("Failed pruning expired storage bucket backups: %w", err)
			}

			return nil
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.BackupsExpire, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating expired backups operation", logger.Ctx{"err": err})
			return
		}

		logger.Info("Pruning expired backups")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting expired backups operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed pruning expired backups", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done pruning expired backups")
	}

	f(context.Background())

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Hour

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func pruneExpiredInstanceBackups(ctx context.Context, s *state.State) error {
	var backups []db.InstanceBackup

	// Get the list of expired backups.
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		backups, err = tx.GetExpiredInstanceBackups(ctx)
		return err
	})
	if err != nil {
		return fmt.Errorf("Unable to retrieve the list of expired instance backups: %w", err)
	}

	for _, b := range backups {
		inst, err := instance.LoadByID(s, b.InstanceID)
		if err != nil {
			return fmt.Errorf("Error loading instance for deleting backup %q: %w", b.Name, err)
		}

		instBackup := backup.NewInstanceBackup(s, inst, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.InstanceOnly, b.OptimizedStorage)
		err = instBackup.Delete()
		if err != nil {
			return fmt.Errorf("Error deleting instance backup %q: %w", b.Name, err)
		}
	}

	return nil
}

func volumeBackupCreate(s *state.State, args db.StoragePoolVolumeBackup, projectName string, poolName string, volumeName string) error {
	l := logger.AddContext(logger.Ctx{"project": projectName, "storage_volume": volumeName, "name": args.Name})
	l.Debug("Volume backup started")
	defer l.Debug("Volume backup finished")

	revert := revert.New()
	defer revert.Fail()

	// Get storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
	}

	// Ignore requests for optimized backups when pool driver doesn't support it.
	if args.OptimizedStorage && !pool.Driver().Info().OptimizedBackups {
		args.OptimizedStorage = false
	}

	// Create the database entry.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.CreateStoragePoolVolumeBackup(ctx, args)
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("Backup %q already exists", args.Name)
		}

		return fmt.Errorf("Failed creating backup record: %w", err)
	}

	revert.Add(func() {
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.DeleteStoragePoolVolumeBackup(ctx, args.Name)
		})
	})

	var backupRow db.StoragePoolVolumeBackup

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		backupRow, err = tx.GetStoragePoolVolumeBackup(ctx, projectName, poolName, args.Name)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting backup record: %w", err)
	}

	// Detect compression method.
	var compress string

	backupRow.CompressionAlgorithm = args.CompressionAlgorithm

	if backupRow.CompressionAlgorithm != "" {
		compress = backupRow.CompressionAlgorithm
	} else {
		compress = s.GlobalConfig.BackupsCompressionAlgorithm()
	}

	// Create the target path if needed.
	backupsPath := internalUtil.VarPath("backups", "custom", pool.Name(), project.StorageVolume(projectName, volumeName))
	if !util.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = os.Remove(backupsPath) })
	}

	target := internalUtil.VarPath("backups", "custom", pool.Name(), project.StorageVolume(projectName, backupRow.Name))

	// Setup the tarball writer.
	l.Debug("Opening backup tarball for writing", logger.Ctx{"path": target})
	tarFileWriter, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("Error opening backup tarball for writing %q: %w", target, err)
	}

	defer func() { _ = tarFileWriter.Close() }()
	revert.Add(func() { _ = os.Remove(target) })

	// Create the tarball.
	tarPipeReader, tarPipeWriter := io.Pipe()
	defer func() { _ = tarPipeWriter.Close() }() // Ensure that go routine below always ends.
	tarWriter := instancewriter.NewInstanceTarWriter(tarPipeWriter, nil)

	// Setup tar writer go routine, with optional compression.
	tarWriterRes := make(chan error)
	var compressErr error

	go func(resCh chan<- error) {
		l.Debug("Started backup tarball writer")
		defer l.Debug("Finished backup tarball writer")
		if compress != "none" {
			compressErr = compressFile(compress, tarPipeReader, tarFileWriter)

			// If a compression error occurred, close the tarPipeWriter to end the export.
			if compressErr != nil {
				_ = tarPipeWriter.Close()
			}
		} else {
			_, err = io.Copy(tarFileWriter, tarPipeReader)
		}

		resCh <- err
	}(tarWriterRes)

	// Write index file.
	l.Debug("Adding backup index file")
	err = volumeBackupWriteIndex(s, projectName, volumeName, pool, backupRow.OptimizedStorage, !backupRow.VolumeOnly, tarWriter)

	// Check compression errors.
	if compressErr != nil {
		return compressErr
	}

	// Check backupWriteIndex for errors.
	if err != nil {
		return fmt.Errorf("Error writing backup index file: %w", err)
	}

	err = pool.BackupCustomVolume(projectName, volumeName, tarWriter, backupRow.OptimizedStorage, !backupRow.VolumeOnly, nil)
	if err != nil {
		return fmt.Errorf("Backup create: %w", err)
	}

	// Close off the tarball file.
	err = tarWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball writer: %w", err)
	}

	// Close off the tarball pipe writer (this will end the go routine above).
	err = tarPipeWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball pipe writer: %w", err)
	}

	err = <-tarWriterRes
	if err != nil {
		return fmt.Errorf("Error writing tarball: %w", err)
	}

	err = tarFileWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tar file: %w", err)
	}

	revert.Success()
	return nil
}

// volumeBackupWriteIndex generates an index.yaml file and then writes it to the root of the backup tarball.
func volumeBackupWriteIndex(s *state.State, projectName string, volumeName string, pool storagePools.Pool, optimized bool, snapshots bool, tarWriter *instancewriter.InstanceTarWriter) error {
	// Indicate whether the driver will include a driver-specific optimized header.
	poolDriverOptimizedHeader := false
	if optimized {
		poolDriverOptimizedHeader = pool.Driver().Info().OptimizedBackupHeader
	}

	config, err := pool.GenerateCustomVolumeBackupConfig(projectName, volumeName, snapshots, nil)
	if err != nil {
		return fmt.Errorf("Failed generating volume backup config: %w", err)
	}

	indexInfo := backup.Info{
		Name:             config.Volume.Name,
		Pool:             pool.Name(),
		Backend:          pool.Driver().Info().Name,
		OptimizedStorage: &optimized,
		OptimizedHeader:  &poolDriverOptimizedHeader,
		Type:             backup.TypeCustom,
		Config:           config,
	}

	if snapshots {
		indexInfo.Snapshots = make([]string, 0, len(config.VolumeSnapshots))
		for _, s := range config.VolumeSnapshots {
			indexInfo.Snapshots = append(indexInfo.Snapshots, s.Name)
		}
	}

	// Convert to YAML.
	indexData, err := yaml.Marshal(indexInfo)
	if err != nil {
		return err
	}

	r := bytes.NewReader(indexData)

	indexFileInfo := instancewriter.FileInfo{
		FileName:    "backup/index.yaml",
		FileSize:    int64(len(indexData)),
		FileMode:    0644,
		FileModTime: time.Now(),
	}

	// Write to tarball.
	err = tarWriter.WriteFileFromReader(r, &indexFileInfo)
	if err != nil {
		return err
	}

	return nil
}

func pruneExpiredStorageVolumeBackups(ctx context.Context, s *state.State) error {
	var volumeBackups []*backup.VolumeBackup

	// Get the list of expired backups.
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		nodeID := tx.GetNodeID()

		backups, err := tx.GetExpiredStorageVolumeBackups(ctx)
		if err != nil {
			return fmt.Errorf("Unable to retrieve the list of expired storage volume backups: %w", err)
		}

		for _, b := range backups {
			vol, err := tx.GetStoragePoolVolumeWithID(ctx, int(b.VolumeID))
			if err != nil {
				logger.Warn("Failed getting storage pool of backup", logger.Ctx{"backup": b.Name, "err": err})
				continue
			}

			// Ignore volumes on other nodes, but include remote pools (NodeID == -1).
			if vol.NodeID != -1 && vol.NodeID != nodeID {
				continue
			}

			volBackup := backup.NewVolumeBackup(s, vol.ProjectName, vol.PoolName, vol.Name, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.VolumeOnly, b.OptimizedStorage)

			volumeBackups = append(volumeBackups, volBackup)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// The deletion is done outside of the transaction to avoid any unnecessary IO while inside of
	// the transaction.
	for _, b := range volumeBackups {
		err := b.Delete()
		if err != nil {
			return fmt.Errorf("Error deleting storage volume backup %q: %w", b.Name(), err)
		}
	}

	return nil
}

func bucketBackupCreate(s *state.State, args db.StoragePoolBucketBackup, projectName string, poolName string, bucketName string) error {
	l := logger.AddContext(logger.Ctx{"project": projectName, "storage_bucket": bucketName, "name": args.Name})
	l.Debug("Bucket backup started")
	defer l.Debug("Bucket backup finished")

	reverter := revert.New()
	defer reverter.Fail()

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return fmt.Errorf("Failed loading storage pool %q: %w", poolName, err)
	}

	// Create the database entry
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.CreateStoragePoolBucketBackup(ctx, args)
	})
	if err != nil {
		if err == db.ErrAlreadyDefined {
			return fmt.Errorf("Backup %q already exists", args.Name)
		}

		return fmt.Errorf("Failed creating backup record: %w", err)
	}

	reverter.Add(func() {
		_ = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.DeleteStoragePoolBucketBackup(ctx, args.Name)
		})
	})

	var backupRow db.StoragePoolBucketBackup
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		backupRow, err = tx.GetStoragePoolBucketBackup(ctx, projectName, poolName, args.Name)
		return err
	})
	if err != nil {
		return fmt.Errorf("Failed getting backup record: %w", err)
	}

	// Detect compression method
	var compress string

	backupRow.CompressionAlgorithm = args.CompressionAlgorithm

	if backupRow.CompressionAlgorithm != "" {
		compress = backupRow.CompressionAlgorithm
	} else {
		compress = s.GlobalConfig.BackupsCompressionAlgorithm()
	}

	// Create the target path if needed.
	backupsPath := internalUtil.VarPath("backups", "buckets", pool.Name(), project.StorageBucket(projectName, bucketName))
	if !util.PathExists(backupsPath) {
		err := os.MkdirAll(backupsPath, 0700)
		if err != nil {
			return err
		}

		reverter.Add(func() { _ = os.Remove(backupsPath) })
	}

	target := internalUtil.VarPath("backups", "buckets", pool.Name(), project.StorageBucket(projectName, backupRow.Name))

	// Setup the tarball writer.
	l.Debug("Opening backup tarball for writing", logger.Ctx{"path": target})
	tarFileWriter, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("Error opening backup tarball for writing %q: %w", target, err)
	}

	defer func() { _ = tarFileWriter.Close() }()
	reverter.Add(func() { _ = os.Remove(target) })

	// Create the tarball.
	tarPipeReader, tarPipeWriter := io.Pipe()
	defer func() { _ = tarPipeWriter.Close() }() // Ensure that go routine below always ends.
	tarWriter := instancewriter.NewInstanceTarWriter(tarPipeWriter, nil)

	// Setup tar writer go routine, with optional compression.
	tarWriterRes := make(chan error)
	var compressErr error

	go func(resCh chan<- error) {
		l.Debug("Started backup tarball writer")
		defer l.Debug("Finished backup tarball writer")
		if compress != "none" {
			compressErr = compressFile(compress, tarPipeReader, tarFileWriter)

			// If a compression error occurred, close the tarPipeWriter to end the export.
			if compressErr != nil {
				_ = tarPipeWriter.Close()
			}
		} else {
			_, err = io.Copy(tarFileWriter, tarPipeReader)
		}

		resCh <- err
	}(tarWriterRes)

	// Write index file.
	l.Debug("Adding backup index file")
	err = bucketBackupWriteIndex(s, projectName, bucketName, pool, tarWriter)

	// Check compression errors.
	if compressErr != nil {
		return compressErr
	}

	// Check backupWriteIndex for errors.
	if err != nil {
		return fmt.Errorf("Error writing backup index file: %w", err)
	}

	err = pool.BackupBucket(projectName, bucketName, tarWriter, nil)
	if err != nil {
		return fmt.Errorf("Backup create: %w", err)
	}

	// Close off the tarball file.
	err = tarWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball writer: %w", err)
	}

	// Close off the tarball pipe writer (this will end the go routine above).
	err = tarPipeWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tarball pipe writer: %w", err)
	}

	err = <-tarWriterRes
	if err != nil {
		return fmt.Errorf("Error writing tarball: %w", err)
	}

	err = tarFileWriter.Close()
	if err != nil {
		return fmt.Errorf("Error closing tar file: %w", err)
	}

	reverter.Success()
	return nil
}

// bucketBackupWriteIndex generates an index.yaml file and then writes it to the root of the backup tarball.
func bucketBackupWriteIndex(s *state.State, projectName string, bucketName string, pool storagePools.Pool, tarWriter *instancewriter.InstanceTarWriter) error {
	config, err := pool.GenerateBucketBackupConfig(projectName, bucketName, nil)
	if err != nil {
		return fmt.Errorf("Failed generating storage backup config: %w", err)
	}

	indexInfo := backup.Info{
		Name:    config.Bucket.Name,
		Pool:    pool.Name(),
		Backend: pool.Driver().Info().Name,
		Type:    backup.TypeBucket,
		Config:  config,
	}

	// Convert to YAML.
	indexData, err := yaml.Marshal(indexInfo)
	if err != nil {
		return err
	}

	r := bytes.NewReader(indexData)

	indexFileInfo := instancewriter.FileInfo{
		FileName:    "backup/index.yaml",
		FileSize:    int64(len(indexData)),
		FileMode:    0644,
		FileModTime: time.Now(),
	}

	// Write to tarball.
	err = tarWriter.WriteFileFromReader(r, &indexFileInfo)
	if err != nil {
		return err
	}

	return nil
}

func pruneExpiredStorageBucketBackups(ctx context.Context, s *state.State) error {
	var bucketBackups []*backup.BucketBackup

	// Get the list of expired backups.
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		backups, err := tx.GetExpiredStorageBucketBackups(ctx)
		if err != nil {
			return fmt.Errorf("Unable to retrieve the list of expired storage bucket backups: %w", err)
		}

		for _, b := range backups {
			bucket, err := tx.GetStoragePoolBucketWithID(ctx, int(b.BucketID))
			if err != nil {
				logger.Warn("Failed getting storage pool of backup", logger.Ctx{"backup": b.Name, "err": err})
				continue
			}

			bucketBackup := backup.NewBucketBackup(s, bucket.Project, bucket.PoolName, bucket.Name, b.ID, b.Name, b.CreationDate, b.ExpiryDate)

			bucketBackups = append(bucketBackups, bucketBackup)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// The deletion is done outside of the transaction to avoid any unnecessary IO while inside of
	// the transaction.
	for _, b := range bucketBackups {
		err := b.Delete()
		if err != nil {
			return fmt.Errorf("Error deleting storage volume backup %q: %w", b.Name(), err)
		}
	}

	return nil
}
