package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/kballard/go-shellquote"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/internal/filter"
	internalInstance "github.com/lxc/incus/internal/instance"
	internalIO "github.com/lxc/incus/internal/io"
	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/node"
	"github.com/lxc/incus/internal/server/operations"
	projectutils "github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/internal/server/task"
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/archive"
	"github.com/lxc/incus/shared/ioprogress"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/osarch"
	"github.com/lxc/incus/shared/util"
)

var imagesCmd = APIEndpoint{
	Path: "images",

	Get:  APIEndpointAction{Handler: imagesGet, AllowUntrusted: true},
	Post: APIEndpointAction{Handler: imagesPost, AllowUntrusted: true},
}

var imageCmd = APIEndpoint{
	Path: "images/{fingerprint}",

	Delete: APIEndpointAction{Handler: imageDelete, AccessHandler: allowPermission(auth.ObjectTypeImage, auth.EntitlementCanEdit, "fingerprint")},
	Get:    APIEndpointAction{Handler: imageGet, AllowUntrusted: true},
	Patch:  APIEndpointAction{Handler: imagePatch, AccessHandler: allowPermission(auth.ObjectTypeImage, auth.EntitlementCanEdit, "fingerprint")},
	Put:    APIEndpointAction{Handler: imagePut, AccessHandler: allowPermission(auth.ObjectTypeImage, auth.EntitlementCanEdit, "fingerprint")},
}

var imageExportCmd = APIEndpoint{
	Path: "images/{fingerprint}/export",

	Get:  APIEndpointAction{Handler: imageExport, AllowUntrusted: true},
	Post: APIEndpointAction{Handler: imageExportPost, AccessHandler: allowPermission(auth.ObjectTypeImage, auth.EntitlementCanEdit, "fingerprint")},
}

var imageSecretCmd = APIEndpoint{
	Path: "images/{fingerprint}/secret",

	Post: APIEndpointAction{Handler: imageSecret, AccessHandler: allowPermission(auth.ObjectTypeImage, auth.EntitlementCanEdit, "fingerprint")},
}

var imageRefreshCmd = APIEndpoint{
	Path: "images/{fingerprint}/refresh",

	Post: APIEndpointAction{Handler: imageRefresh, AccessHandler: allowPermission(auth.ObjectTypeImage, auth.EntitlementCanEdit, "fingerprint")},
}

var imageAliasesCmd = APIEndpoint{
	Path: "images/aliases",

	Get:  APIEndpointAction{Handler: imageAliasesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: imageAliasesPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateImageAliases)},
}

var imageAliasCmd = APIEndpoint{
	Path: "images/aliases/{name:.*}",

	Delete: APIEndpointAction{Handler: imageAliasDelete, AccessHandler: allowPermission(auth.ObjectTypeImageAlias, auth.EntitlementCanEdit, "name")},
	Get:    APIEndpointAction{Handler: imageAliasGet, AllowUntrusted: true},
	Patch:  APIEndpointAction{Handler: imageAliasPatch, AccessHandler: allowPermission(auth.ObjectTypeImageAlias, auth.EntitlementCanEdit, "name")},
	Post:   APIEndpointAction{Handler: imageAliasPost, AccessHandler: allowPermission(auth.ObjectTypeImageAlias, auth.EntitlementCanEdit, "name")},
	Put:    APIEndpointAction{Handler: imageAliasPut, AccessHandler: allowPermission(auth.ObjectTypeImageAlias, auth.EntitlementCanEdit, "name")},
}

/*
We only want a single publish running at any one time.

	The CPU and I/O load of publish is such that running multiple ones in
	parallel takes longer than running them serially.

	Additionally, publishing the same container or container snapshot
	twice would lead to storage problem, not to mention a conflict at the
	end for whichever finishes last.
*/
var imagePublishLock sync.Mutex

// imageTaskMu prevents image related tasks from being scheduled at the same time as each other to prevent them
// stepping on each other's toes.
var imageTaskMu sync.Mutex

func compressFile(compress string, infile io.Reader, outfile io.Writer) error {
	reproducible := []string{"gzip"}
	var cmd *exec.Cmd

	// Parse the command.
	fields, err := shellquote.Split(compress)
	if err != nil {
		return err
	}

	if fields[0] == "squashfs" {
		// 'tar2sqfs' do not support writing to stdout. So write to a temporary
		//  file first and then replay the compressed content to outfile.
		tempfile, err := os.CreateTemp("", "incus_compress_")
		if err != nil {
			return err
		}

		defer func() { _ = tempfile.Close() }()
		defer func() { _ = os.Remove(tempfile.Name()) }()

		// Prepare 'tar2sqfs' arguments
		args := []string{"tar2sqfs"}
		if len(fields) > 1 {
			args = append(args, fields[1:]...)
		}

		args = append(args, "--no-skip", "--force", "--compressor", "xz", tempfile.Name())
		cmd = exec.Command(args[0], args[1:]...)
		cmd.Stdin = infile

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("tar2sqfs: %v (%v)", err, strings.TrimSpace(string(output)))
		}
		// Replay the result to outfile
		_, err = tempfile.Seek(0, io.SeekStart)
		if err != nil {
			return err
		}

		_, err = io.Copy(outfile, tempfile)
		if err != nil {
			return err
		}
	} else {
		args := []string{"-c"}
		if len(fields) > 1 {
			args = append(args, fields[1:]...)
		}

		if util.ValueInSlice(fields[0], reproducible) {
			args = append(args, "-n")
		}

		cmd := exec.Command(fields[0], args...)
		cmd.Stdin = infile
		cmd.Stdout = outfile
		err := cmd.Run()
		if err != nil {
			return err
		}
	}

	return nil
}

/*
 * This function takes a container or snapshot from the local image server and
 * exports it as an image.
 */
func imgPostInstanceInfo(s *state.State, r *http.Request, req api.ImagesPost, op *operations.Operation, builddir string, budget int64) (*api.Image, error) {
	info := api.Image{}
	info.Properties = map[string]string{}
	projectName := request.ProjectParam(r)
	name := req.Source.Name
	ctype := req.Source.Type
	if ctype == "" || name == "" {
		return nil, fmt.Errorf("No source provided")
	}

	switch ctype {
	case "snapshot":
		if !internalInstance.IsSnapshot(name) {
			return nil, fmt.Errorf("Not a snapshot")
		}

	case "container", "virtual-machine", "instance":
		if internalInstance.IsSnapshot(name) {
			return nil, fmt.Errorf("This is a snapshot")
		}

	default:
		return nil, fmt.Errorf("Bad type")
	}

	info.Filename = req.Filename
	switch req.Public {
	case true:
		info.Public = true
	case false:
		info.Public = false
	}

	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return nil, err
	}

	info.Type = c.Type().String()

	// Build the actual image file
	imageFile, err := os.CreateTemp(builddir, "incus_build_image_")
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.Remove(imageFile.Name()) }()

	// Calculate (close estimate of) total size of input to image
	totalSize := int64(0)
	sumSize := func(path string, fi os.FileInfo, err error) error {
		if err == nil {
			totalSize += fi.Size()
		}

		return nil
	}

	err = filepath.Walk(c.RootfsPath(), sumSize)
	if err != nil {
		return nil, err
	}

	// Track progress creating image.
	metadata := make(map[string]any)
	imageProgressWriter := &ioprogress.ProgressWriter{
		Tracker: &ioprogress.ProgressTracker{
			Handler: func(value, speed int64) {
				percent := int64(0)
				var processed int64

				if totalSize > 0 {
					percent = value
					processed = totalSize * (percent / 100.0)
				} else {
					processed = value
				}

				operations.SetProgressMetadata(metadata, "create_image_from_container_pack", "Image pack", percent, processed, speed)
				_ = op.UpdateMetadata(metadata)
			},
			Length: totalSize,
		},
	}

	sha256 := sha256.New()
	var compress string
	var writer io.Writer

	if req.CompressionAlgorithm != "" {
		compress = req.CompressionAlgorithm
	} else {
		var p *api.Project
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			project, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
			if err != nil {
				return err
			}

			p, err = project.ToAPI(ctx, tx.Tx())

			return err
		})
		if err != nil {
			return nil, err
		}

		if p.Config["images.compression_algorithm"] != "" {
			compress = p.Config["images.compression_algorithm"]
		} else {
			compress = s.GlobalConfig.ImagesCompressionAlgorithm()
		}
	}

	// Setup tar, optional compress and sha256 to happen in one pass.
	wg := sync.WaitGroup{}
	var compressErr error
	if compress != "none" {
		wg.Add(1)
		tarReader, tarWriter := io.Pipe()
		imageProgressWriter.WriteCloser = tarWriter
		writer = imageProgressWriter
		compressWriter := io.MultiWriter(imageFile, sha256)
		go func() {
			defer wg.Done()
			compressErr = compressFile(compress, tarReader, compressWriter)

			// If a compression error occurred, close the writer to end the instance export.
			if compressErr != nil {
				_ = imageProgressWriter.Close()
			}
		}()
	} else {
		imageProgressWriter.WriteCloser = imageFile
		writer = io.MultiWriter(imageProgressWriter, sha256)
	}

	// Export instance to writer.
	var meta api.ImageMetadata

	writer = internalIO.NewQuotaWriter(writer, budget)
	meta, err = c.Export(writer, req.Properties, req.ExpiresAt)

	// Get ExpiresAt
	if meta.ExpiryDate != 0 {
		info.ExpiresAt = time.Unix(meta.ExpiryDate, 0)
	}

	// Clean up file handles.
	// When compression is used, Close on imageProgressWriter/tarWriter is required for compressFile/gzip to
	// know it is finished. Otherwise it is equivalent to imageFile.Close.
	_ = imageProgressWriter.Close()
	wg.Wait() // Wait until compression helper has finished if used.
	_ = imageFile.Close()

	// Check compression errors.
	if compressErr != nil {
		return nil, compressErr
	}

	// Check instance export errors.
	if err != nil {
		return nil, err
	}

	fi, err := os.Stat(imageFile.Name())
	if err != nil {
		return nil, err
	}

	info.Size = fi.Size()
	info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))
	info.CreatedAt = time.Now().UTC()

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, _, err = tx.GetImage(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if !response.IsNotFoundError(err) {
		if err != nil {
			return nil, err
		}

		return &info, fmt.Errorf("The image already exists: %s", info.Fingerprint)
	}

	/* rename the file to the expected name so our caller can use it */
	finalName := internalUtil.VarPath("images", info.Fingerprint)
	err = internalUtil.FileMove(imageFile.Name(), finalName)
	if err != nil {
		return nil, err
	}

	info.Architecture, _ = osarch.ArchitectureName(c.Architecture())
	info.Properties = meta.Properties

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the database entry
		return tx.CreateImage(ctx, c.Project().Name, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, info.Type, nil)
	})
	if err != nil {
		return nil, err
	}

	return &info, nil
}

func imgPostRemoteInfo(s *state.State, r *http.Request, req api.ImagesPost, op *operations.Operation, project string, budget int64) (*api.Image, error) {
	var err error
	var hash string

	if req.Source.Fingerprint != "" {
		hash = req.Source.Fingerprint
	} else if req.Source.Alias != "" {
		hash = req.Source.Alias
	} else {
		return nil, fmt.Errorf("must specify one of alias or fingerprint for init from image")
	}

	info, err := ImageDownload(r, s, op, &ImageDownloadArgs{
		Server:            req.Source.Server,
		Protocol:          req.Source.Protocol,
		Certificate:       req.Source.Certificate,
		Secret:            req.Source.Secret,
		Alias:             hash,
		Type:              req.Source.ImageType,
		AutoUpdate:        req.AutoUpdate,
		Public:            req.Public,
		ProjectName:       project,
		Budget:            budget,
		SourceProjectName: req.Source.Project,
	})
	if err != nil {
		return nil, err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var id int

		id, info, err = tx.GetImage(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &project})
		if err != nil {
			return err
		}

		// Allow overriding or adding properties
		for k, v := range req.Properties {
			info.Properties[k] = v
		}

		// Get profile IDs
		if req.Profiles == nil {
			req.Profiles = []string{api.ProjectDefaultName}
		}

		profileIds := make([]int64, len(req.Profiles))

		for i, profile := range req.Profiles {
			profileID, _, err := tx.GetProfile(ctx, project, profile)
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Profile '%s' doesn't exist", profile)
			} else if err != nil {
				return err
			}

			profileIds[i] = profileID
		}

		// Update the DB record if needed
		if req.Public || req.AutoUpdate || req.Filename != "" || len(req.Properties) > 0 || len(req.Profiles) > 0 {
			err := tx.UpdateImage(ctx, id, req.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, project, profileIds)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return info, nil
}

func imgPostURLInfo(s *state.State, r *http.Request, req api.ImagesPost, op *operations.Operation, project string, budget int64) (*api.Image, error) {
	var err error

	if req.Source.URL == "" {
		return nil, fmt.Errorf("Missing URL")
	}

	myhttp, err := localUtil.HTTPClient("", s.Proxy)
	if err != nil {
		return nil, err
	}

	// Resolve the image URL
	head, err := http.NewRequest("HEAD", req.Source.URL, nil)
	if err != nil {
		return nil, err
	}

	architectures := []string{}
	for _, architecture := range s.OS.Architectures {
		architectureName, err := osarch.ArchitectureName(architecture)
		if err != nil {
			return nil, err
		}

		architectures = append(architectures, architectureName)
	}

	head.Header.Set("User-Agent", version.UserAgent)
	head.Header.Set("Incus-Server-Architectures", strings.Join(architectures, ", "))
	head.Header.Set("Incus-Server-Version", version.Version)

	raw, err := myhttp.Do(head)
	if err != nil {
		return nil, err
	}

	hash := raw.Header.Get("Incus-Image-Hash")
	if hash == "" {
		return nil, fmt.Errorf("Missing Incus-Image-Hash header")
	}

	url := raw.Header.Get("Incus-Image-URL")
	if url == "" {
		return nil, fmt.Errorf("Missing Incus-Image-URL header")
	}

	// Import the image
	info, err := ImageDownload(r, s, op, &ImageDownloadArgs{
		Server:      url,
		Protocol:    "direct",
		Alias:       hash,
		AutoUpdate:  req.AutoUpdate,
		Public:      req.Public,
		ProjectName: project,
		Budget:      budget,
	})
	if err != nil {
		return nil, err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var id int

		id, info, err = tx.GetImage(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &project})
		if err != nil {
			return err
		}

		// Allow overriding or adding properties
		for k, v := range req.Properties {
			info.Properties[k] = v
		}

		if req.Public || req.AutoUpdate || req.Filename != "" || len(req.Properties) > 0 {
			err := tx.UpdateImage(ctx, id, req.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, "", nil)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return info, nil
}

func getImgPostInfo(s *state.State, r *http.Request, builddir string, project string, post *os.File, metadata map[string]any) (*api.Image, error) {
	info := api.Image{}
	var imageMeta *api.ImageMetadata
	l := logger.AddContext(logger.Ctx{"function": "getImgPostInfo"})

	info.Public = util.IsTrue(r.Header.Get("X-Incus-public"))
	propHeaders := r.Header[http.CanonicalHeaderKey("X-Incus-properties")]
	profilesHeaders := r.Header.Get("X-Incus-profiles")
	ctype, ctypeParams, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		ctype = "application/octet-stream"
	}

	sha256 := sha256.New()
	var size int64

	if ctype == "multipart/form-data" {
		// Create a temporary file for the image tarball
		imageTarf, err := os.CreateTemp(builddir, "incus_tar_")
		if err != nil {
			return nil, err
		}

		defer func() { _ = os.Remove(imageTarf.Name()) }()

		// Parse the POST data
		_, err = post.Seek(0, io.SeekStart)
		if err != nil {
			return nil, err
		}

		mr := multipart.NewReader(post, ctypeParams["boundary"])

		// Get the metadata tarball
		part, err := mr.NextPart()
		if err != nil {
			return nil, err
		}

		if part.FormName() != "metadata" {
			return nil, fmt.Errorf("Invalid multipart image")
		}

		size, err = io.Copy(io.MultiWriter(imageTarf, sha256), part)
		info.Size += size

		_ = imageTarf.Close()
		if err != nil {
			l.Error("Failed to copy the image tarfile", logger.Ctx{"err": err})
			return nil, err
		}

		// Get the rootfs tarball
		part, err = mr.NextPart()
		if err != nil {
			l.Error("Failed to get the next part", logger.Ctx{"err": err})
			return nil, err
		}

		if part.FormName() == "rootfs" {
			info.Type = instancetype.Container.String()
		} else if part.FormName() == "rootfs.img" {
			info.Type = instancetype.VM.String()
		} else {
			l.Error("Invalid multipart image")
			return nil, fmt.Errorf("Invalid multipart image")
		}

		// Create a temporary file for the rootfs tarball
		rootfsTarf, err := os.CreateTemp(builddir, "incus_tar_")
		if err != nil {
			return nil, err
		}

		defer func() { _ = os.Remove(rootfsTarf.Name()) }()

		size, err = io.Copy(io.MultiWriter(rootfsTarf, sha256), part)
		info.Size += size

		_ = rootfsTarf.Close()
		if err != nil {
			l.Error("Failed to copy the rootfs tarfile", logger.Ctx{"err": err})
			return nil, err
		}

		info.Filename = part.FileName()
		info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

		expectedFingerprint := r.Header.Get("X-Incus-fingerprint")
		if expectedFingerprint != "" && info.Fingerprint != expectedFingerprint {
			err = fmt.Errorf("fingerprints don't match, got %s expected %s", info.Fingerprint, expectedFingerprint)
			return nil, err
		}

		imageMeta, _, err = getImageMetadata(imageTarf.Name())
		if err != nil {
			l.Error("Failed to get image metadata", logger.Ctx{"err": err})
			return nil, err
		}

		imgfname := internalUtil.VarPath("images", info.Fingerprint)
		err = internalUtil.FileMove(imageTarf.Name(), imgfname)
		if err != nil {
			l.Error("Failed to move the image tarfile", logger.Ctx{
				"err":    err,
				"source": imageTarf.Name(),
				"dest":   imgfname})
			return nil, err
		}

		rootfsfname := internalUtil.VarPath("images", info.Fingerprint+".rootfs")
		err = internalUtil.FileMove(rootfsTarf.Name(), rootfsfname)
		if err != nil {
			l.Error("Failed to move the rootfs tarfile", logger.Ctx{
				"err":    err,
				"source": rootfsTarf.Name(),
				"dest":   imgfname})
			return nil, err
		}
	} else {
		_, err = post.Seek(0, io.SeekStart)
		if err != nil {
			return nil, err
		}

		size, err = io.Copy(sha256, post)
		if err != nil {
			l.Error("Failed to copy the tarfile", logger.Ctx{"err": err})
			return nil, err
		}

		info.Size = size

		info.Filename = r.Header.Get("X-Incus-filename")
		info.Fingerprint = fmt.Sprintf("%x", sha256.Sum(nil))

		expectedFingerprint := r.Header.Get("X-Incus-fingerprint")
		if expectedFingerprint != "" && info.Fingerprint != expectedFingerprint {
			l.Error("Fingerprints don't match", logger.Ctx{
				"got":      info.Fingerprint,
				"expected": expectedFingerprint})
			err = fmt.Errorf("fingerprints don't match, got %s expected %s", info.Fingerprint, expectedFingerprint)
			return nil, err
		}

		var imageType string
		imageMeta, imageType, err = getImageMetadata(post.Name())
		if err != nil {
			l.Error("Failed to get image metadata", logger.Ctx{"err": err})
			return nil, err
		}

		info.Type = imageType

		imgfname := internalUtil.VarPath("images", info.Fingerprint)
		err = internalUtil.FileMove(post.Name(), imgfname)
		if err != nil {
			l.Error("Failed to move the tarfile", logger.Ctx{
				"err":    err,
				"source": post.Name(),
				"dest":   imgfname})
			return nil, err
		}
	}

	info.Architecture = imageMeta.Architecture
	info.CreatedAt = time.Unix(imageMeta.CreationDate, 0)

	expiresAt, ok := metadata["expires_at"]
	if ok {
		info.ExpiresAt = expiresAt.(time.Time)
	} else {
		info.ExpiresAt = time.Unix(imageMeta.ExpiryDate, 0)
	}

	properties, ok := metadata["properties"]
	if ok {
		info.Properties = properties.(map[string]string)
	} else {
		info.Properties = imageMeta.Properties
	}

	if len(propHeaders) > 0 {
		for _, ph := range propHeaders {
			p, _ := url.ParseQuery(ph)
			for pkey, pval := range p {
				info.Properties[pkey] = pval[0]
			}
		}
	}

	var profileIds []int64
	if len(profilesHeaders) > 0 {
		p, _ := url.ParseQuery(profilesHeaders)
		profileIds = make([]int64, len(p["profile"]))

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			for i, val := range p["profile"] {
				profileID, _, err := tx.GetProfile(ctx, project, val)
				if response.IsNotFoundError(err) {
					return fmt.Errorf("Profile '%s' doesn't exist", val)
				} else if err != nil {
					return err
				}

				profileIds[i] = profileID
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var exists bool

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if the image already exists
		exists, err = tx.ImageExists(ctx, project, info.Fingerprint)

		return err
	})
	if err != nil {
		return nil, err
	}

	if exists {
		// Do not create a database entry if the request is coming from the internal
		// cluster communications for image synchronization
		if isClusterNotification(r) {
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.AddImageToLocalNode(ctx, project, info.Fingerprint)
			})
			if err != nil {
				return nil, err
			}
		} else {
			return &info, fmt.Errorf("Image with same fingerprint already exists")
		}
	} else {
		public, ok := metadata["public"]
		if ok {
			info.Public = public.(bool)
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Create the database entry
			return tx.CreateImage(ctx, project, info.Fingerprint, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, info.Type, profileIds)
		})
		if err != nil {
			return nil, err
		}
	}

	return &info, nil
}

// imageCreateInPool() creates a new storage volume in a given storage pool for
// the image. No entry in the images database will be created. This implies that
// imageCreateinPool() should only be called when an image already exists in the
// database and hence has already a storage volume in at least one storage pool.
func imageCreateInPool(s *state.State, info *api.Image, storagePool string) error {
	if storagePool == "" {
		return fmt.Errorf("No storage pool specified")
	}

	pool, err := storagePools.LoadByName(s, storagePool)
	if err != nil {
		return err
	}

	err = pool.EnsureImage(info.Fingerprint, nil)
	if err != nil {
		return err
	}

	return nil
}

// swagger:operation POST /1.0/images?public images images_post_untrusted
//
//  Add an image
//
//  Pushes the data to the target image server.
//  This is meant for server to server communication where a new image entry is
//  prepared on the target server and the source server is provided that URL
//  and a secret token to push the image content over.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: body
//      name: image
//      description: Image
//      required: true
//      schema:
//        $ref: "#/definitions/ImagesPost"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation POST /1.0/images images images_post
//
//	Add an image
//
//	Adds a new image to the image store.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image
//	    required: false
//	    schema:
//	      $ref: "#/definitions/ImagesPost"
//	  - in: body
//	    name: raw_image
//	    description: Raw image file
//	    required: false
//	  - in: header
//	    name: X-Incus-secret
//	    description: Push secret for server to server communication
//	    schema:
//	      type: string
//	    example: RANDOM-STRING
//	  - in: header
//	    name: X-Incus-fingerprint
//	    description: Expected fingerprint when pushing a raw image
//	    schema:
//	      type: string
//	  - in: header
//	    name: X-Incus-properties
//	    description: Descriptive properties
//	    schema:
//	      type: object
//	      additionalProperties:
//	        type: string
//	  - in: header
//	    name: X-Incus-public
//	    description: Whether the image is available to unauthenticated users
//	    schema:
//	      type: boolean
//	  - in: header
//	    name: X-Incus-filename
//	    description: Original filename of the image
//	    schema:
//	      type: string
//	  - in: header
//	    name: X-Incus-profiles
//	    description: List of profiles to use
//	    schema:
//	      type: array
//	      items:
//	        type: string
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	var userCanCreateImages bool
	err := s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectProject(projectName), auth.EntitlementCanCreateImages)
	if err == nil {
		userCanCreateImages = true
	} else if !api.StatusErrorCheck(err, http.StatusForbidden) {
		return response.SmartError(err)
	}

	trusted := d.checkTrustedClient(r) == nil && userCanCreateImages

	secret := r.Header.Get("X-Incus-secret")
	fingerprint := r.Header.Get("X-Incus-fingerprint")

	var imageMetadata map[string]any

	if !trusted && (secret == "" || fingerprint == "") {
		return response.Forbidden(nil)
	} else {
		// We need to invalidate the secret whether the source is trusted or not.
		op, err := imageValidSecret(s, r, projectName, fingerprint, secret)
		if err != nil {
			return response.SmartError(err)
		}

		if op != nil {
			imageMetadata = op.Metadata
		} else if !trusted {
			return response.Forbidden(nil)
		}
	}

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	// create a directory under which we keep everything while building
	builddir, err := os.MkdirTemp(internalUtil.VarPath("images"), "incus_build_")
	if err != nil {
		return response.InternalError(err)
	}

	cleanup := func(path string, fd *os.File) {
		if fd != nil {
			_ = fd.Close()
		}

		err := os.RemoveAll(path)
		if err != nil {
			logger.Debugf("Error deleting temporary directory \"%s\": %s", path, err)
		}
	}

	// Store the post data to disk
	post, err := os.CreateTemp(builddir, "incus_post_")
	if err != nil {
		cleanup(builddir, nil)
		return response.InternalError(err)
	}

	// Possibly set a quota on the amount of disk space this project is
	// allowed to use.
	var budget int64
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		budget, err = projectutils.GetImageSpaceBudget(tx, projectName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	_, err = io.Copy(internalIO.NewQuotaWriter(post, budget), r.Body)
	if err != nil {
		logger.Errorf("Store image POST data to disk: %v", err)
		cleanup(builddir, post)
		return response.InternalError(err)
	}

	// Is this a container request?
	_, err = post.Seek(0, io.SeekStart)
	if err != nil {
		return response.InternalError(err)
	}

	decoder := json.NewDecoder(post)
	imageUpload := false

	req := api.ImagesPost{}
	err = decoder.Decode(&req)
	if err != nil {
		if r.Header.Get("Content-Type") == "application/json" {
			cleanup(builddir, post)
			return response.BadRequest(err)
		}

		imageUpload = true
	}

	if !imageUpload && req.Source.Mode == "push" {
		cleanup(builddir, post)

		metadata := map[string]any{
			"aliases":    req.Aliases,
			"expires_at": req.ExpiresAt,
			"properties": req.Properties,
			"public":     req.Public,
		}

		return createTokenResponse(s, r, projectName, req.Source.Fingerprint, metadata)
	}

	if !imageUpload && !util.ValueInSlice(req.Source.Type, []string{"container", "instance", "virtual-machine", "snapshot", "image", "url"}) {
		cleanup(builddir, post)
		return response.InternalError(fmt.Errorf("Invalid images JSON"))
	}

	/* Forward requests for containers on other nodes */
	if !imageUpload && util.ValueInSlice(req.Source.Type, []string{"container", "instance", "virtual-machine", "snapshot"}) {
		name := req.Source.Name
		if name != "" {
			_, err = post.Seek(0, io.SeekStart)
			if err != nil {
				return response.InternalError(err)
			}

			r.Body = post
			resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
			if err != nil {
				cleanup(builddir, post)
				return response.SmartError(err)
			}

			if resp != nil {
				cleanup(builddir, nil)
				return resp
			}
		}
	}

	// Begin background operation
	run := func(op *operations.Operation) error {
		var err error
		var info *api.Image

		// Setup the cleanup function
		defer cleanup(builddir, post)

		if imageUpload {
			/* Processing image upload */
			info, err = getImgPostInfo(s, r, builddir, projectName, post, imageMetadata)
		} else {
			if req.Source.Type == "image" {
				/* Processing image copy from remote */
				info, err = imgPostRemoteInfo(s, r, req, op, projectName, budget)
			} else if req.Source.Type == "url" {
				/* Processing image copy from URL */
				info, err = imgPostURLInfo(s, r, req, op, projectName, budget)
			} else {
				/* Processing image creation from container */
				imagePublishLock.Lock()
				info, err = imgPostInstanceInfo(s, r, req, op, builddir, budget)
				imagePublishLock.Unlock()
			}
		}

		// Set the metadata if possible, even if there is an error
		if info != nil {
			metadata := make(map[string]string)
			metadata["fingerprint"] = info.Fingerprint
			metadata["size"] = strconv.FormatInt(info.Size, 10)

			// Keep secret if available
			secret, ok := op.Metadata()["secret"]
			if ok {
				metadata["secret"] = secret.(string)
			}

			_ = op.UpdateMetadata(metadata)
		}

		if err != nil {
			return err
		}

		if isClusterNotification(r) {
			// If dealing with in-cluster image copy, don't touch the database.
			return nil
		}

		// Apply any provided alias
		aliases, ok := imageMetadata["aliases"]
		if ok {
			req.Aliases = aliases.([]api.ImageAlias)
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			imgID, _, err := tx.GetImageByFingerprintPrefix(ctx, info.Fingerprint, dbCluster.ImageFilter{Project: &projectName})
			if err != nil {
				return fmt.Errorf("Fetch image %q: %w", info.Fingerprint, err)
			}

			for _, alias := range req.Aliases {
				_, _, err := tx.GetImageAlias(ctx, projectName, alias.Name, true)
				if !response.IsNotFoundError(err) {
					if err != nil {
						return fmt.Errorf("Fetch image alias %q: %w", alias.Name, err)
					}

					return fmt.Errorf("Alias already exists: %s", alias.Name)
				}

				err = tx.CreateImageAlias(ctx, projectName, alias.Name, imgID, alias.Description)
				if err != nil {
					return fmt.Errorf("Add new image alias to the database: %w", err)
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Sync the images between each node in the cluster on demand
		err = imageSyncBetweenNodes(s, r, projectName, info.Fingerprint)
		if err != nil {
			return fmt.Errorf("Failed syncing image between nodes: %w", err)
		}

		// Add the image to the authorizer.
		err = s.Authorizer.AddImage(s.ShutdownCtx, projectName, info.Fingerprint)
		if err != nil {
			logger.Error("Failed to add image to authorizer", logger.Ctx{"fingerprint": info.Fingerprint, "project": projectName, "error": err})
		}

		s.Events.SendLifecycle(projectName, lifecycle.ImageCreated.Event(info.Fingerprint, projectName, op.Requestor(), logger.Ctx{"type": info.Type}))

		return nil
	}

	var metadata any

	if imageUpload && imageMetadata != nil {
		secret, _ := internalUtil.RandomHexString(32)
		if secret != "" {
			metadata = map[string]string{
				"secret": secret,
			}
		}
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.ImageDownload, nil, metadata, run, nil, nil, r)
	if err != nil {
		cleanup(builddir, post)
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func getImageMetadata(fname string) (*api.ImageMetadata, string, error) {
	var tr *tar.Reader
	var result api.ImageMetadata

	// Open the file
	r, err := os.Open(fname)
	if err != nil {
		return nil, "unknown", err
	}

	defer func() { _ = r.Close() }()

	// Decompress if needed
	_, algo, unpacker, err := archive.DetectCompressionFile(r)
	if err != nil {
		return nil, "unknown", err
	}

	_, err = r.Seek(0, io.SeekStart)
	if err != nil {
		return nil, "", err
	}

	if unpacker == nil {
		return nil, "unknown", fmt.Errorf("Unsupported backup compression")
	}

	// Open the tarball
	if len(unpacker) > 0 {
		if algo == ".squashfs" {
			// sqfs2tar can only read from a file
			unpacker = append(unpacker, fname)
		}

		cmd := exec.Command(unpacker[0], unpacker[1:]...)
		cmd.Stdin = r

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, "unknown", err
		}

		defer func() { _ = stdout.Close() }()

		err = cmd.Start()
		if err != nil {
			return nil, "unknown", err
		}

		defer func() { _ = cmd.Wait() }()

		// Double close stdout, this is to avoid blocks in Wait()
		defer func() { _ = stdout.Close() }()

		tr = tar.NewReader(stdout)
	} else {
		tr = tar.NewReader(r)
	}

	// Parse the content
	hasMeta := false
	hasRoot := false
	imageType := "unknown"
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}

		if err != nil {
			return nil, "unknown", err
		}

		if hdr.Name == "metadata.yaml" || hdr.Name == "./metadata.yaml" {
			err = yaml.NewDecoder(tr).Decode(&result)
			if err != nil {
				return nil, "unknown", err
			}

			hasMeta = true
		}

		if strings.HasPrefix(hdr.Name, "rootfs/") || strings.HasPrefix(hdr.Name, "./rootfs/") {
			hasRoot = true
			imageType = instancetype.Container.String()
		}

		if hdr.Name == "rootfs.img" || hdr.Name == "./rootfs.img" {
			hasRoot = true
			imageType = instancetype.VM.String()
		}

		if hasMeta && hasRoot {
			// Done with the bits we want, no need to keep reading
			break
		}
	}

	if !hasMeta {
		return nil, "unknown", fmt.Errorf("Metadata tarball is missing metadata.yaml")
	}

	_, err = osarch.ArchitectureId(result.Architecture)
	if err != nil {
		return nil, "unknown", err
	}

	if result.CreationDate == 0 {
		return nil, "unknown", fmt.Errorf("Missing creation date")
	}

	return &result, imageType, nil
}

func doImagesGet(ctx context.Context, tx *db.ClusterTx, recursion bool, projectName string, public bool, clauses *filter.ClauseSet, hasPermission auth.PermissionChecker) (any, error) {
	mustLoadObjects := recursion || (clauses != nil && len(clauses.Clauses) > 0)

	fingerprints, err := tx.GetImagesFingerprints(ctx, projectName, public)
	if err != nil {
		return err, err
	}

	var resultString []string
	var resultMap []*api.Image

	if recursion {
		resultMap = make([]*api.Image, 0, len(fingerprints))
	} else {
		resultString = make([]string, 0, len(fingerprints))
	}

	for _, fingerprint := range fingerprints {
		image, err := doImageGet(ctx, tx, projectName, fingerprint, public)
		if err != nil {
			continue
		}

		if !image.Public && !hasPermission(auth.ObjectImage(projectName, fingerprint)) {
			continue
		}

		if !mustLoadObjects {
			resultString = append(resultString, api.NewURL().Path(version.APIVersion, "images", fingerprint).String())
		} else {
			if clauses != nil && len(clauses.Clauses) > 0 {
				match, err := filter.Match(*image, *clauses)
				if err != nil {
					return nil, err
				}

				if !match {
					continue
				}
			}

			if recursion {
				resultMap = append(resultMap, image)
			} else {
				resultString = append(resultString, api.NewURL().Path(version.APIVersion, "images", image.Fingerprint).String())
			}
		}
	}

	if recursion {
		return resultMap, nil
	}

	return resultString, nil
}

// swagger:operation GET /1.0/images?public images images_get_untrusted
//
//  Get the public images
//
//  Returns a list of publicly available images (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/images/06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb",
//                "/1.0/images/084dd79dd1360fd25a2479eb46674c2a5ef3022a40fe03c91ab3603e3402b8e1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images?public&recursion=1 images images_get_recursion1_untrusted
//
//  Get the public images
//
//  Returns a list of publicly available images (structs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of images
//            items:
//              $ref: "#/definitions/Image"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images images images_get
//
//  Get the images
//
//  Returns a list of images (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/images/06b86454720d36b20f94e31c6812e05ec51c1b568cf3a8abd273769d213394bb",
//                "/1.0/images/084dd79dd1360fd25a2479eb46674c2a5ef3022a40fe03c91ab3603e3402b8e1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images?recursion=1 images images_get_recursion1
//
//	Get the images
//
//	Returns a list of images (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: filter
//	    description: Collection filter
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of images
//	          items:
//	            $ref: "#/definitions/Image"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagesGet(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	filterStr := r.FormValue("filter")

	s := d.State()

	hasPermission, authorizationErr := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeImage)
	if authorizationErr != nil && !api.StatusErrorCheck(authorizationErr, http.StatusForbidden) {
		return response.SmartError(authorizationErr)
	}

	public := d.checkTrustedClient(r) != nil || authorizationErr != nil

	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.SmartError(fmt.Errorf("Invalid filter: %w", err))
	}

	var result any
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		result, err = doImagesGet(ctx, tx, localUtil.IsRecursionRequest(r), projectName, public, clauses, hasPermission)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, result)
}

func autoUpdateImagesTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		opRun := func(op *operations.Operation) error {
			return autoUpdateImages(ctx, s)
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ImagesUpdate, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating image update operation", logger.Ctx{"err": err})
			return
		}

		logger.Debug("Acquiring image task lock")
		imageTaskMu.Lock()
		defer imageTaskMu.Unlock()
		logger.Debug("Acquired image task lock")

		logger.Info("Updating images")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting image update operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed updating images", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done updating images")
	}

	return f, task.Hourly()
}

func autoUpdateImages(ctx context.Context, s *state.State) error {
	imageMap := make(map[string][]dbCluster.Image)

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		autoUpdate := true
		images, err := dbCluster.GetImages(ctx, tx.Tx(), dbCluster.ImageFilter{AutoUpdate: &autoUpdate})
		if err != nil {
			return err
		}

		for _, image := range images {
			imageMap[image.Fingerprint] = append(imageMap[image.Fingerprint], image)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Unable to retrieve image fingerprints: %w", err)
	}

	for fingerprint, images := range imageMap {
		skipFingerprint := false

		var nodes []string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			nodes, err = tx.GetNodesWithImageAndAutoUpdate(ctx, fingerprint, true)

			return err
		})
		if err != nil {
			logger.Error("Error getting cluster members for image auto-update", logger.Ctx{"fingerprint": fingerprint, "err": err})
			continue
		}

		if len(nodes) > 1 {
			var nodeIDs []int64

			for _, node := range nodes {
				err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					var err error

					nodeInfo, err := tx.GetNodeByAddress(ctx, node)
					if err != nil {
						return err
					}

					nodeIDs = append(nodeIDs, nodeInfo.ID)

					return nil
				})
				if err != nil {
					logger.Error("Unable to retrieve cluster member information for image update", logger.Ctx{"err": err})
					skipFingerprint = true
					break
				}
			}

			if skipFingerprint {
				continue
			}

			// If multiple nodes have the image, select one to deal with it.
			if len(nodeIDs) > 1 {
				selectedNode, err := localUtil.GetStableRandomInt64FromList(int64(len(images)), nodeIDs)
				if err != nil {
					logger.Error("Failed to select cluster member for image update", logger.Ctx{"err": err})
					continue
				}

				// Skip image update if we're not the chosen cluster member.
				// That way, an image is only updated by a single cluster member.
				if s.DB.Cluster.GetNodeID() != selectedNode {
					continue
				}
			}
		}

		var deleteIDs []int
		var newImage *api.Image

		for _, image := range images {
			filter := dbCluster.ImageFilter{Project: &image.Project}
			if image.Public {
				filter.Public = &image.Public
			}

			var imageInfo *api.Image

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				_, imageInfo, err = tx.GetImage(ctx, image.Fingerprint, filter)

				return err
			})
			if err != nil {
				logger.Error("Failed to get image", logger.Ctx{"err": err, "project": image.Project, "fingerprint": image.Fingerprint})
				continue
			}

			newInfo, err := autoUpdateImage(ctx, s, nil, image.ID, imageInfo, image.Project, false)
			if err != nil {
				logger.Error("Failed to update image", logger.Ctx{"err": err, "project": image.Project, "fingerprint": image.Fingerprint})

				if err == context.Canceled {
					return nil
				}
			} else {
				deleteIDs = append(deleteIDs, image.ID)
			}

			// newInfo will have the same content for each image in the list.
			// Therefore, we just pick the first.
			if newImage == nil {
				newImage = newInfo
			}
		}

		if newImage != nil {
			if len(nodes) > 1 {
				err := distributeImage(ctx, s, nodes, fingerprint, newImage)
				if err != nil {
					logger.Error("Failed to distribute new image", logger.Ctx{"err": err, "fingerprint": newImage.Fingerprint})

					if err == context.Canceled {
						return nil
					}
				}
			}

			_ = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				for _, ID := range deleteIDs {
					// Remove the database entry for the image after distributing to cluster members.
					err := tx.DeleteImage(ctx, ID)
					if err != nil {
						logger.Error("Error deleting old image from database", logger.Ctx{"err": err, "fingerprint": fingerprint, "ID": ID})
					}
				}

				return nil
			})

		}
	}

	return nil
}

func distributeImage(ctx context.Context, s *state.State, nodes []string, oldFingerprint string, newImage *api.Image) error {
	// Get config of all nodes (incl. own) and check for storage.images_volume.
	// If the setting is missing, distribute the image to the node.
	// If the option is set, only distribute the image once to nodes with this
	// specific pool/volume.

	// imageVolumes is a list containing of all image volumes specified by
	// storage.images_volume. Since this option is node specific, the values
	// may be different for each cluster member.
	var imageVolumes []string

	err := s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		vol := config.StorageImagesVolume()
		if vol != "" {
			fields := strings.Split(vol, "/")

			var pool *api.StoragePool

			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, pool, _, err = tx.GetStoragePool(ctx, fields[0])

				return err
			})
			if err != nil {
				return fmt.Errorf("Failed to get storage pool info: %w", err)
			}

			// Add the volume to the list if the pool is backed by remote
			// storage as only then the volumes are shared.
			if util.ValueInSlice(pool.Driver, db.StorageRemoteDriverNames()) {
				imageVolumes = append(imageVolumes, vol)
			}
		}

		return nil
	})
	// No need to return with an error as this is only an optimization in the
	// distribution process. Instead, only log the error.
	if err != nil {
		logger.Error("Failed to load config", logger.Ctx{"err": err})
	}

	// Skip own node
	localClusterAddress := s.LocalConfig.ClusterAddress()

	var poolIDs []int64
	var poolNames []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the IDs of all storage pools on which a storage volume
		// for the requested image currently exists.
		poolIDs, err = tx.GetPoolsWithImage(ctx, newImage.Fingerprint)
		if err != nil {
			logger.Error("Error getting image storage pools", logger.Ctx{"err": err, "fingerprint": oldFingerprint})
			return err
		}

		// Translate the IDs to poolNames.
		poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
		if err != nil {
			logger.Error("Error getting image storage pools", logger.Ctx{"err": err, "fingerprint": oldFingerprint})
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	for _, nodeAddress := range nodes {
		if nodeAddress == localClusterAddress {
			continue
		}

		var nodeInfo db.NodeInfo
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			nodeInfo, err = tx.GetNodeByAddress(ctx, nodeAddress)
			return err
		})
		if err != nil {
			return fmt.Errorf("Failed to retrieve information about cluster member with address %q: %w", nodeAddress, err)
		}

		client, err := cluster.Connect(nodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
		if err != nil {
			return fmt.Errorf("Failed to connect to %q for image synchronization: %w", nodeAddress, err)
		}

		client = client.UseTarget(nodeInfo.Name)

		resp, _, err := client.GetServer()
		if err != nil {
			logger.Error("Failed to retrieve information about cluster member", logger.Ctx{"err": err, "remote": nodeAddress})
		} else {
			vol := resp.Config["storage.images_volume"]
			skipDistribution := false

			// If storage.images_volume is set on the cluster member, check if
			// the image has already been downloaded to this volume. If so,
			// skip distributing the image to this cluster member.
			// If the option is unset, distribute the image.
			if vol != "" {
				for _, imageVolume := range imageVolumes {
					if imageVolume == vol {
						skipDistribution = true
						break
					}
				}

				if skipDistribution {
					continue
				}

				fields := strings.Split(vol, "/")

				pool, _, err := client.GetStoragePool(fields[0])
				if err != nil {
					logger.Error("Failed to get storage pool info", logger.Ctx{"err": err, "pool": fields[0]})
				} else {
					if util.ValueInSlice(pool.Driver, db.StorageRemoteDriverNames()) {
						imageVolumes = append(imageVolumes, vol)
					}
				}
			}
		}

		createArgs := &incus.ImageCreateArgs{}
		imageMetaPath := internalUtil.VarPath("images", newImage.Fingerprint)
		imageRootfsPath := internalUtil.VarPath("images", newImage.Fingerprint+".rootfs")

		metaFile, err := os.Open(imageMetaPath)
		if err != nil {
			return err
		}

		defer func() { _ = metaFile.Close() }()

		createArgs.MetaFile = metaFile
		createArgs.MetaName = filepath.Base(imageMetaPath)
		createArgs.Type = newImage.Type

		if util.PathExists(imageRootfsPath) {
			rootfsFile, err := os.Open(imageRootfsPath)
			if err != nil {
				return err
			}

			defer func() { _ = rootfsFile.Close() }()

			createArgs.RootfsFile = rootfsFile
			createArgs.RootfsName = filepath.Base(imageRootfsPath)
		}

		image := api.ImagesPost{}
		image.Filename = createArgs.MetaName

		op, err := client.CreateImage(image, createArgs)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			_ = op.Cancel()
			return ctx.Err()
		default:
		}

		err = op.Wait()
		if err != nil {
			return err
		}

		for _, poolName := range poolNames {
			if poolName == "" {
				continue
			}

			req := internalImageOptimizePost{
				Image: *newImage,
				Pool:  poolName,
			}

			_, _, err = client.RawQuery("POST", "/internal/image-optimize", req, "")
			if err != nil {
				logger.Error("Failed creating new image in storage pool", logger.Ctx{"err": err, "remote": nodeAddress, "pool": poolName, "fingerprint": newImage.Fingerprint})
			}

			err = client.DeleteStoragePoolVolume(poolName, "image", oldFingerprint)
			if err != nil {
				logger.Error("Failed deleting old image from storage pool", logger.Ctx{"err": err, "remote": nodeAddress, "pool": poolName, "fingerprint": oldFingerprint})
			}
		}
	}

	return nil
}

// Update a single image.  The operation can be nil, if no progress tracking is needed.
// Returns whether the image has been updated.
func autoUpdateImage(ctx context.Context, s *state.State, op *operations.Operation, id int, info *api.Image, projectName string, manual bool) (*api.Image, error) {
	fingerprint := info.Fingerprint
	var source api.ImageSource

	if !manual {
		var interval int64

		var project *api.Project
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			p, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
			if err != nil {
				return err
			}

			project, err = p.ToAPI(ctx, tx.Tx())
			return err
		})
		if err != nil {
			return nil, err
		}

		if project.Config["images.auto_update_interval"] != "" {
			interval, err = strconv.ParseInt(project.Config["images.auto_update_interval"], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("Unable to fetch project configuration: %w", err)
			}
		} else {
			interval = s.GlobalConfig.ImagesAutoUpdateIntervalHours()
		}

		// Check if we're supposed to auto update at all (0 disables it)
		if interval <= 0 {
			return nil, nil
		}

		now := time.Now()
		elapsedHours := int64(math.Round(now.Sub(s.StartTime).Hours()))
		if elapsedHours%interval != 0 {
			return nil, nil
		}
	}

	var poolNames []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		_, source, err = tx.GetImageSource(ctx, id)
		if err != nil {
			logger.Error("Error getting source image", logger.Ctx{"err": err, "fingerprint": fingerprint})
			return err
		}

		// Get the IDs of all storage pools on which a storage volume
		// for the requested image currently exists.
		poolIDs, err := tx.GetPoolsWithImage(ctx, fingerprint)
		if err != nil {
			logger.Error("Error getting image pools", logger.Ctx{"err": err, "fingerprint": fingerprint})
			return err
		}

		// Translate the IDs to poolNames.
		poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
		if err != nil {
			logger.Error("Error getting image pools", logger.Ctx{"err": err, "fingerprint": fingerprint})
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// If no optimized pools at least update the base store
	if len(poolNames) == 0 {
		poolNames = append(poolNames, "")
	}

	logger.Debug("Processing image", logger.Ctx{"fingerprint": fingerprint, "server": source.Server, "protocol": source.Protocol, "alias": source.Alias})

	// Set operation metadata to indicate whether a refresh happened
	setRefreshResult := func(result bool) {
		if op == nil {
			return
		}

		metadata := map[string]any{"refreshed": result}
		_ = op.UpdateMetadata(metadata)

		// Sent a lifecycle event if the refresh actually happened.
		if result {
			s.Events.SendLifecycle(projectName, lifecycle.ImageRefreshed.Event(fingerprint, projectName, op.Requestor(), nil))
		}
	}

	// Update the image on each pool where it currently exists.
	hash := fingerprint
	var newInfo *api.Image

	for _, poolName := range poolNames {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		newInfo, err = ImageDownload(nil, s, op, &ImageDownloadArgs{
			Server:      source.Server,
			Protocol:    source.Protocol,
			Certificate: source.Certificate,
			Alias:       source.Alias,
			Type:        info.Type,
			AutoUpdate:  true,
			Public:      info.Public,
			StoragePool: poolName,
			ProjectName: projectName,
			Budget:      -1,
		})
		if err != nil {
			logger.Error("Failed to update the image", logger.Ctx{"err": err, "fingerprint": fingerprint})
			continue
		}

		hash = newInfo.Fingerprint
		if hash == fingerprint {
			logger.Debug("Image already up to date", logger.Ctx{"fingerprint": fingerprint})
			continue
		}

		var newID int

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			newID, _, err = tx.GetImage(ctx, hash, dbCluster.ImageFilter{Project: &projectName})

			return err
		})
		if err != nil {
			logger.Error("Error loading image", logger.Ctx{"err": err, "fingerprint": hash})
			continue
		}

		if info.Cached {
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.SetImageCachedAndLastUseDate(ctx, projectName, hash, info.LastUsedAt)
			})
			if err != nil {
				logger.Error("Error setting cached flag and last use date", logger.Ctx{"err": err, "fingerprint": hash})
				continue
			}
		} else {
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				err = tx.UpdateImageLastUseDate(ctx, projectName, hash, info.LastUsedAt)
				if err != nil {
					logger.Error("Error setting last use date", logger.Ctx{"err": err, "fingerprint": hash})
					return err
				}

				return nil
			})
			if err != nil {
				continue
			}
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.MoveImageAlias(ctx, id, newID)
		})
		if err != nil {
			logger.Error("Error moving aliases", logger.Ctx{"err": err, "fingerprint": hash})
			continue
		}

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.CopyDefaultImageProfiles(ctx, id, newID)
		})
		if err != nil {
			logger.Error("Copying default profiles", logger.Ctx{"err": err, "fingerprint": hash})
		}

		// If we do have optimized pools, make sure we remove the volumes associated with the image.
		if poolName != "" {
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				logger.Error("Error loading storage pool to delete image", logger.Ctx{"err": err, "pool": poolName, "fingerprint": fingerprint})
				continue
			}

			err = pool.DeleteImage(fingerprint, op)
			if err != nil {
				logger.Error("Error deleting image from storage pool", logger.Ctx{"err": err, "pool": pool.Name(), "fingerprint": fingerprint})
				continue
			}
		}
	}

	// Image didn't change, nothing to do.
	if hash == fingerprint {
		setRefreshResult(false)
		return nil, nil
	}

	// Remove main image file.
	fname := filepath.Join(s.OS.VarDir, "images", fingerprint)
	if util.PathExists(fname) {
		err = os.Remove(fname)
		if err != nil {
			logger.Error("Error deleting image file", logger.Ctx{"fingerprint": fingerprint, "file": fname, "err": err})
		}
	}

	// Remove the rootfs file for the image.
	fname = filepath.Join(s.OS.VarDir, "images", fingerprint) + ".rootfs"
	if util.PathExists(fname) {
		err = os.Remove(fname)
		if err != nil {
			logger.Error("Error deleting image rootfs file", logger.Ctx{"fingerprint": fingerprint, "file": fname, "err": err})
		}
	}

	setRefreshResult(true)
	return newInfo, nil
}

func pruneExpiredImagesTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		opRun := func(op *operations.Operation) error {
			return pruneExpiredImages(ctx, s, op)
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ImagesExpire, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating expired image prune operation", logger.Ctx{"err": err})
			return
		}

		logger.Debug("Acquiring image task lock")
		imageTaskMu.Lock()
		defer imageTaskMu.Unlock()
		logger.Debug("Acquired image task lock")

		logger.Info("Pruning expired images")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting expired image prune operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed expiring images", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done pruning expired images")
	}

	// Skip the first run, and instead run an initial pruning synchronously
	// before we start updating images later on in the start up process.
	f(context.Background())

	first := true
	schedule := func() (time.Duration, error) {
		interval := 24 * time.Hour
		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

func pruneLeftoverImages(s *state.State) {
	opRun := func(op *operations.Operation) error {
		// Check if dealing with shared image storage.
		var storageImages string
		err := s.DB.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
			nodeConfig, err := node.ConfigLoad(ctx, tx)
			if err != nil {
				return err
			}

			storageImages = nodeConfig.StorageImagesVolume()

			return nil
		})
		if err != nil {
			return err
		}

		if storageImages != "" {
			// Parse the source.
			poolName, _, err := daemonStorageSplitVolume(storageImages)
			if err != nil {
				return err
			}

			// Load the pool.
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				return err
			}

			// Skip cleanup if image volume may be multi-node.
			// When such a volume is used, we may have images that are
			// tied to other servers in the shared images folder and don't want to
			// delete those.
			if pool.Driver().Info().VolumeMultiNode {
				return nil
			}
		}

		// Get all images
		var images []string
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			images, err = tx.GetLocalImagesFingerprints(ctx)
			return err
		})
		if err != nil {
			return fmt.Errorf("Unable to retrieve the list of images: %w", err)
		}

		// Look at what's in the images directory
		entries, err := os.ReadDir(internalUtil.VarPath("images"))
		if err != nil {
			return fmt.Errorf("Unable to list the images directory: %w", err)
		}

		// Check and delete leftovers
		for _, entry := range entries {
			fp := strings.Split(entry.Name(), ".")[0]
			if !util.ValueInSlice(fp, images) {
				err = os.RemoveAll(internalUtil.VarPath("images", entry.Name()))
				if err != nil {
					return fmt.Errorf("Unable to remove leftover image: %v: %w", entry.Name(), err)
				}

				logger.Debugf("Removed leftover image file: %s", entry.Name())
			}
		}

		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ImagesPruneLeftover, nil, nil, opRun, nil, nil, nil)
	if err != nil {
		logger.Error("Failed creating leftover image clean up operation", logger.Ctx{"err": err})
		return
	}

	logger.Debug("Acquiring image task lock")
	imageTaskMu.Lock()
	defer imageTaskMu.Unlock()
	logger.Debug("Acquired image task lock")

	logger.Info("Cleaning up leftover image files")
	err = op.Start()
	if err != nil {
		logger.Error("Failed starting leftover image clean up operation", logger.Ctx{"err": err})
		return
	}

	err = op.Wait(s.ShutdownCtx)
	if err != nil {
		logger.Error("Failed cleaning up leftover image files", logger.Ctx{"err": err})
		return
	}

	logger.Infof("Done cleaning up leftover image files")
}

func pruneExpiredImages(ctx context.Context, s *state.State, op *operations.Operation) error {
	var err error
	var projectsImageRemoteCacheExpiryDays map[string]int64
	var allImages map[string][]dbCluster.Image

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get an image remote cache expiry days value for each project and store keyed on project name.
		globalImageRemoteCacheExpiryDays := s.GlobalConfig.ImagesRemoteCacheExpiryDays()

		dbProjects, err := dbCluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		projectsImageRemoteCacheExpiryDays = make(map[string]int64, len(dbProjects))
		for _, p := range dbProjects {
			p, err := p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// If there is a project specific image expiry set use that.
			if p.Config["images.remote_cache_expiry"] != "" {
				expiry, err := strconv.ParseInt(p.Config["images.remote_cache_expiry"], 10, 64)
				if err != nil {
					return fmt.Errorf("Unable to fetch project configuration: %w", err)
				}

				projectsImageRemoteCacheExpiryDays[p.Name] = expiry
			} else {
				// Otherwise use the global default.
				projectsImageRemoteCacheExpiryDays[p.Name] = globalImageRemoteCacheExpiryDays
			}
		}

		// Get all cached images across all projects and store them keyed on fingerprint.
		cached := true
		images, err := dbCluster.GetImages(ctx, tx.Tx(), dbCluster.ImageFilter{Cached: &cached})
		if err != nil {
			return fmt.Errorf("Failed getting images: %w", err)
		}

		allImages = make(map[string][]dbCluster.Image, len(images))
		for _, image := range images {
			allImages[image.Fingerprint] = append(allImages[image.Fingerprint], image)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Unable to retrieve project names: %w", err)
	}

	for fingerprint, dbImages := range allImages {
		// At each iteration we check if we got cancelled in the meantime. It is safe to abort here since
		// anything not expired now will be expired at the next run.
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		dbImagesDeleted := 0
		for _, dbImage := range dbImages {
			// Get expiry days for image's project.
			expiryDays := projectsImageRemoteCacheExpiryDays[dbImage.Project]

			// Skip if no project expiry time set.
			if expiryDays <= 0 {
				continue
			}

			// Figure out the expiry of image.
			timestamp := dbImage.UploadDate
			if !dbImage.LastUseDate.Time.IsZero() {
				timestamp = dbImage.LastUseDate.Time
			}

			imageExpiry := timestamp.Add(time.Duration(expiryDays) * time.Hour * 24)

			// Skip if image is not expired.
			if imageExpiry.After(time.Now()) {
				continue
			}

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Remove the database entry for the image.
				return tx.DeleteImage(ctx, dbImage.ID)
			})
			if err != nil {
				return fmt.Errorf("Error deleting image %q in project %q from database: %w", fingerprint, dbImage.Project, err)
			}

			dbImagesDeleted++

			logger.Info("Deleted expired cached image record", logger.Ctx{"fingerprint": fingerprint, "project": dbImage.Project, "expiry": imageExpiry})

			s.Events.SendLifecycle(dbImage.Project, lifecycle.ImageDeleted.Event(fingerprint, dbImage.Project, op.Requestor(), nil))
		}

		// Skip deleting the image files and image storage volumes on disk if image is not expired in all
		// of its projects.
		if dbImagesDeleted < len(dbImages) {
			continue
		}

		var poolIDs []int64
		var poolNames []string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get the IDs of all storage pools on which a storage volume for the image currently exists.
			poolIDs, err = tx.GetPoolsWithImage(ctx, fingerprint)
			if err != nil {
				return err
			}

			// Translate the IDs to poolNames.
			poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			continue
		}

		for _, poolName := range poolNames {
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				return fmt.Errorf("Error loading storage pool %q to delete image volume %q: %w", poolName, fingerprint, err)
			}

			err = pool.DeleteImage(fingerprint, op)
			if err != nil {
				return fmt.Errorf("Error deleting image volume %q from storage pool %q: %w", fingerprint, pool.Name(), err)
			}
		}

		// Remove main image file.
		fname := filepath.Join(s.OS.VarDir, "images", fingerprint)
		err = os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Error deleting image file %q: %w", fname, err)
		}

		// Remove the rootfs file for the image.
		fname = filepath.Join(s.OS.VarDir, "images", fingerprint) + ".rootfs"
		err = os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Error deleting image file %q: %w", fname, err)
		}

		logger.Info("Deleted expired cached image files and volumes", logger.Ctx{"fingerprint": fingerprint})
	}

	return nil
}

// swagger:operation DELETE /1.0/images/{fingerprint} images image_delete
//
//	Delete the image
//
//	Removes the image from the image store.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	var imgID int
	var imgInfo *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Use the fingerprint we received in a LIKE query and use the full
		// fingerprint we receive from the database in all further queries.
		imgID, imgInfo, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	do := func(op *operations.Operation) error {
		// Lock this operation to ensure that concurrent image operations don't conflict.
		// Other operations will wait for this one to finish.
		unlock, err := imageOperationLock(imgInfo.Fingerprint)
		if err != nil {
			return err
		}

		defer unlock()

		var exist bool

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check image still exists and another request hasn't removed it since we resolved the image
			// fingerprint above.
			exist, err = tx.ImageExists(ctx, projectName, imgInfo.Fingerprint)

			return err
		})
		if err != nil {
			return err
		}

		if !exist {
			return api.StatusErrorf(http.StatusNotFound, "Image not found")
		}

		if !isClusterNotification(r) {
			var referenced bool

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Check if the image being deleted is actually still
				// referenced by other projects. In that case we don't want to
				// physically delete it just yet, but just to remove the
				// relevant database entry.
				referenced, err = tx.ImageIsReferencedByOtherProjects(ctx, projectName, imgInfo.Fingerprint)
				if err != nil {
					return err
				}

				if referenced {
					err = tx.DeleteImage(ctx, imgID)
					if err != nil {
						return fmt.Errorf("Error deleting image info from the database: %w", err)
					}
				}

				return nil
			})
			if err != nil {
				return err
			}

			if referenced {
				return nil
			}

			// Notify the other nodes about the removed image so they can remove it from disk too.
			notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAll)
			if err != nil {
				return err
			}

			err = notifier(func(client incus.InstanceServer) error {
				op, err := client.UseProject(projectName).DeleteImage(imgInfo.Fingerprint)
				if err != nil {
					return fmt.Errorf("Failed to request to delete image from peer node: %w", err)
				}

				err = op.Wait()
				if err != nil {
					return fmt.Errorf("Failed to delete image from peer node: %w", err)
				}

				return nil
			})
			if err != nil {
				return err
			}
		}

		var poolIDs []int64
		var poolNames []string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Delete the pool volumes.
			poolIDs, err = tx.GetPoolsWithImage(ctx, imgInfo.Fingerprint)
			if err != nil {
				return err
			}

			poolNames, err = tx.GetPoolNamesFromIDs(ctx, poolIDs)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		for _, poolName := range poolNames {
			pool, err := storagePools.LoadByName(s, poolName)
			if err != nil {
				return fmt.Errorf("Error loading storage pool %q to delete image %q: %w", poolName, imgInfo.Fingerprint, err)
			}

			// Only perform the deletion of remote volumes on the server handling the request.
			if !isClusterNotification(r) || !pool.Driver().Info().Remote {
				err = pool.DeleteImage(imgInfo.Fingerprint, op)
				if err != nil {
					return fmt.Errorf("Error deleting image %q from storage pool %q: %w", imgInfo.Fingerprint, pool.Name(), err)
				}
			}
		}

		// Remove the database entry.
		if !isClusterNotification(r) {
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.DeleteImage(ctx, imgID)
			})
			if err != nil {
				return fmt.Errorf("Error deleting image info from the database: %w", err)
			}
		}

		// Remove main image file from disk.
		imageDeleteFromDisk(imgInfo.Fingerprint)

		// Remove image from authorizer.
		err = s.Authorizer.DeleteImage(s.ShutdownCtx, projectName, imgInfo.Fingerprint)
		if err != nil {
			logger.Error("Failed to remove image from authorizer", logger.Ctx{"fingerprint": imgInfo.Fingerprint, "project": projectName, "error": err})
		}

		s.Events.SendLifecycle(projectName, lifecycle.ImageDeleted.Event(imgInfo.Fingerprint, projectName, op.Requestor(), nil))

		return nil
	}

	resources := map[string][]api.URL{}
	resources["images"] = []api.URL{*api.NewURL().Path(version.APIVersion, "images", imgInfo.Fingerprint)}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.ImageDelete, resources, nil, do, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Helper to delete an image file from the local images directory.
func imageDeleteFromDisk(fingerprint string) {
	// Remove main image file.
	fname := internalUtil.VarPath("images", fingerprint)
	if util.PathExists(fname) {
		err := os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			logger.Errorf("Error deleting image file %s: %s", fname, err)
		}
	}

	// Remove the rootfs file for the image.
	fname = internalUtil.VarPath("images", fingerprint) + ".rootfs"
	if util.PathExists(fname) {
		err := os.Remove(fname)
		if err != nil && !os.IsNotExist(err) {
			logger.Errorf("Error deleting image file %s: %s", fname, err)
		}
	}
}

func doImageGet(ctx context.Context, tx *db.ClusterTx, project, fingerprint string, public bool) (*api.Image, error) {
	filter := dbCluster.ImageFilter{Project: &project}
	if public {
		filter.Public = &public
	}

	_, imgInfo, err := tx.GetImageByFingerprintPrefix(ctx, fingerprint, filter)
	if err != nil {
		return nil, err
	}

	return imgInfo, nil
}

// imageValidSecret searches for an ImageToken operation running on any member in the default project that has an
// images resource matching the specified fingerprint and the metadata secret field matches the specified secret.
// If an operation is found it is returned and the operation is cancelled. Otherwise nil is returned if not found.
func imageValidSecret(s *state.State, r *http.Request, projectName string, fingerprint string, secret string) (*api.Operation, error) {
	ops, err := operationsGetByType(s, r, projectName, operationtype.ImageToken)
	if err != nil {
		return nil, fmt.Errorf("Failed getting image token operations: %w", err)
	}

	for _, op := range ops {
		if op.Resources == nil {
			continue
		}

		opImages, ok := op.Resources["images"]
		if !ok {
			continue
		}

		if !util.StringPrefixInSlice(api.NewURL().Path(version.APIVersion, "images", fingerprint).String(), opImages) {
			continue
		}

		opSecret, ok := op.Metadata["secret"]
		if !ok {
			continue
		}

		if opSecret == secret {
			// Token is single-use, so cancel it now.
			err = operationCancel(s, r, projectName, op)
			if err != nil {
				return nil, fmt.Errorf("Failed to cancel operation %q: %w", op.ID, err)
			}

			return op, nil
		}
	}

	return nil, nil
}

// swagger:operation GET /1.0/images/{fingerprint}?public images image_get_untrusted
//
//  Get the public image
//
//  Gets a specific public image.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: secret
//      description: Secret token to retrieve a private image
//      type: string
//      example: RANDOM-STRING
//  responses:
//    "200":
//      description: Image
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            $ref: "#/definitions/Image"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/{fingerprint} images image_get
//
//	Get the image
//
//	Gets a specific image.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Image
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Image"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the image (expand partial fingerprints).
	var info *api.Image
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		info, err = doImageGet(ctx, tx, projectName, fingerprint, false)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	var userCanViewImage bool
	err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectImage(projectName, info.Fingerprint), auth.EntitlementCanView)
	if err == nil {
		userCanViewImage = true
	} else if !api.StatusErrorCheck(err, http.StatusForbidden) {
		return response.SmartError(err)
	}

	public := d.checkTrustedClient(r) != nil || !userCanViewImage
	secret := r.FormValue("secret")

	op, err := imageValidSecret(s, r, projectName, info.Fingerprint, secret)
	if err != nil {
		return response.SmartError(err)
	}

	if !info.Public && public && op == nil {
		return response.NotFound(fmt.Errorf("Image %q not found", info.Fingerprint))
	}

	etag := []any{info.Public, info.AutoUpdate, info.Properties}
	return response.SyncResponseETag(true, info, etag)
}

// swagger:operation PUT /1.0/images/{fingerprint} images image_put
//
//	Update the image
//
//	Updates the entire image definition.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImagePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	var id int
	var info *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, info, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{info.Public, info.AutoUpdate, info.Properties}
	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.ImagePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get ExpiresAt
	if !req.ExpiresAt.IsZero() {
		info.ExpiresAt = req.ExpiresAt
	}

	// Get profile IDs
	if req.Profiles == nil {
		req.Profiles = []string{"default"}
	}

	profileIds := make([]int64, len(req.Profiles))

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		for i, profile := range req.Profiles {
			profileID, _, err := tx.GetProfile(ctx, projectName, profile)
			if response.IsNotFoundError(err) {
				return fmt.Errorf("Profile '%s' doesn't exist", profile)
			} else if err != nil {
				return err
			}

			profileIds[i] = profileID
		}

		return tx.UpdateImage(ctx, id, info.Filename, info.Size, req.Public, req.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, req.Properties, projectName, profileIds)
	})
	if err != nil {
		if response.IsNotFoundError(err) {
			return response.BadRequest(err)
		}

		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.ImageUpdated.Event(info.Fingerprint, projectName, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/images/{fingerprint} images image_patch
//
//	Partially update the image
//
//	Updates a subset of the image definition.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImagePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imagePatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	var id int
	var info *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, info, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{info.Public, info.AutoUpdate, info.Properties}
	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))
	rdr2 := io.NopCloser(bytes.NewBuffer(body))

	reqRaw := jmap.Map{}
	err = json.NewDecoder(rdr1).Decode(&reqRaw)
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.ImagePut{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get AutoUpdate
	autoUpdate, err := reqRaw.GetBool("auto_update")
	if err == nil {
		info.AutoUpdate = autoUpdate
	}

	// Get Public
	public, err := reqRaw.GetBool("public")
	if err == nil {
		info.Public = public
	}

	// Get Properties
	_, ok := reqRaw["properties"]
	if ok {
		properties := req.Properties
		for k, v := range info.Properties {
			_, ok := req.Properties[k]
			if !ok {
				properties[k] = v
			}
		}
		info.Properties = properties
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateImage(ctx, id, info.Filename, info.Size, info.Public, info.AutoUpdate, info.Architecture, info.CreatedAt, info.ExpiresAt, info.Properties, "", nil)
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.ImageUpdated.Event(info.Fingerprint, projectName, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/images/aliases images images_aliases_post
//
//	Add an image alias
//
//	Creates a new image alias.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	req := api.ImageAliasesPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" || req.Target == "" {
		return response.BadRequest(fmt.Errorf("name and target are required"))
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// This is just to see if the alias name already exists.
		_, _, err = tx.GetImageAlias(ctx, projectName, req.Name, true)
		if !response.IsNotFoundError(err) {
			if err != nil {
				return err
			}

			return api.StatusErrorf(http.StatusConflict, "Alias %q already exists", req.Name)
		}

		imgID, _, err := tx.GetImageByFingerprintPrefix(ctx, req.Target, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		err = tx.CreateImageAlias(ctx, projectName, req.Name, imgID, req.Description)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Add the image alias to the authorizer.
	err = s.Authorizer.AddImageAlias(r.Context(), projectName, req.Name)
	if err != nil {
		logger.Error("Failed to add image alias to authorizer", logger.Ctx{"name": req.Name, "project": projectName, "error": err})
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ImageAliasCreated.Event(req.Name, projectName, requestor, logger.Ctx{"target": req.Target})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/images/aliases images images_aliases_get
//
//  Get the image aliases
//
//  Returns a list of image aliases (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/images/aliases/foo",
//                "/1.0/images/aliases/bar1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/aliases?recursion=1 images images_aliases_get_recursion1
//
//	Get the image aliases
//
//	Returns a list of image aliases (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of image aliases
//	          items:
//	            $ref: "#/definitions/ImageAliasesEntry"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasesGet(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	recursion := localUtil.IsRecursionRequest(r)

	s := d.State()
	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeImageAlias)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to get a permission checker: %w", err))
	}

	var responseStr []string
	var responseMap []api.ImageAliasesEntry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		names, err := tx.GetImageAliases(ctx, projectName)
		if err != nil {
			return err
		}

		if recursion {
			responseMap = make([]api.ImageAliasesEntry, 0, len(names))
		} else {
			responseStr = make([]string, 0, len(names))
		}

		for _, name := range names {
			if !userHasPermission(auth.ObjectImageAlias(projectName, name)) {
				continue
			}

			if !recursion {
				responseStr = append(responseStr, api.NewURL().Path(version.APIVersion, "images", "aliases", name).String())
			} else {
				_, alias, err := tx.GetImageAlias(ctx, projectName, name, true)
				if err != nil {
					continue
				}

				responseMap = append(responseMap, alias)
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	if !recursion {
		return response.SyncResponse(true, responseStr)
	}

	return response.SyncResponse(true, responseMap)
}

// swagger:operation GET /1.0/images/aliases/{name}?public images image_alias_get_untrusted
//
//  Get the public image alias
//
//  Gets a specific public image alias.
//  This untrusted endpoint only works for aliases pointing to public images.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//  responses:
//    "200":
//      description: Image alias
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            $ref: "#/definitions/ImageAliasesEntry"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/aliases/{name} images image_alias_get
//
//	Get the image alias
//
//	Gets a specific image alias.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Image alias
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/ImageAliasesEntry"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasGet(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	s := d.State()

	var userCanViewImageAlias bool
	err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectImageAlias(projectName, name), auth.EntitlementCanView)
	if err == nil {
		userCanViewImageAlias = true
	} else if !api.StatusErrorCheck(err, http.StatusForbidden) {
		return response.SmartError(err)
	}

	public := d.checkTrustedClient(r) != nil || !userCanViewImageAlias

	var alias api.ImageAliasesEntry
	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, alias, err = tx.GetImageAlias(ctx, projectName, name, !public)

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, alias, alias)
}

// swagger:operation DELETE /1.0/images/aliases/{name} images image_alias_delete
//
//	Delete the image alias
//
//	Deletes a specific image alias.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, _, err = tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		err = tx.DeleteImageAlias(ctx, projectName, name)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Remove image alias from authorizer.
	err = s.Authorizer.DeleteImageAlias(r.Context(), projectName, name)
	if err != nil {
		logger.Error("Failed to remove image alias from authorizer", logger.Ctx{"name": name, "project": projectName, "error": err})
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.ImageAliasDeleted.Event(name, projectName, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation PUT /1.0/images/aliases/{name} images images_aliases_put
//
//	Update the image alias
//
//	Updates the entire image alias configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesEntryPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ImageAliasesEntryPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Target == "" {
		return response.BadRequest(fmt.Errorf("The target field is required"))
	}

	var imgAlias api.ImageAliasesEntry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var imgAliasID int

		imgAliasID, imgAlias, err = tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		// Validate ETag
		err = localUtil.EtagCheck(r, imgAlias)
		if err != nil {
			return err
		}

		imageID, _, err := tx.GetImageByFingerprintPrefix(ctx, req.Target, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		err = tx.UpdateImageAlias(ctx, imgAliasID, imageID, req.Description)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.ImageAliasUpdated.Event(imgAlias.Name, projectName, requestor, logger.Ctx{"target": req.Target}))

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/images/aliases/{name} images images_alias_patch
//
//	Partially update the image alias
//
//	Updates a subset of the image alias configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesEntryPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasPatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get current value
	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := jmap.Map{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	var imgAlias api.ImageAliasesEntry
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var imgAliasID int
		imgAliasID, imgAlias, err = tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		// Validate ETag
		err = localUtil.EtagCheck(r, imgAlias)
		if err != nil {
			return err
		}

		_, ok := req["target"]
		if ok {
			target, err := req.GetString("target")
			if err != nil {
				return api.StatusErrorf(http.StatusBadRequest, "%v", err)
			}

			imgAlias.Target = target
		}

		_, ok = req["description"]
		if ok {
			description, err := req.GetString("description")
			if err != nil {
				return api.StatusErrorf(http.StatusBadRequest, "%v", err)
			}

			imgAlias.Description = description
		}

		imageID, _, err := tx.GetImage(ctx, imgAlias.Target, dbCluster.ImageFilter{Project: &projectName})
		if err != nil {
			return err
		}

		err = tx.UpdateImageAlias(ctx, imgAliasID, imageID, imgAlias.Description)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.ImageAliasUpdated.Event(imgAlias.Name, projectName, requestor, logger.Ctx{"target": imgAlias.Target}))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/images/aliases/{name} images images_alias_post
//
//	Rename the image alias
//
//	Renames an existing image alias.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image alias
//	    description: Image alias rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageAliasesEntryPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageAliasPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ImageAliasesEntryPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// This is just to see if the alias name already exists.
		_, _, err := tx.GetImageAlias(ctx, projectName, req.Name, true)
		if !response.IsNotFoundError(err) {
			if err != nil {
				return err
			}

			return api.StatusErrorf(http.StatusConflict, "Alias %q already exists", req.Name)
		}

		imgAliasID, _, err := tx.GetImageAlias(ctx, projectName, name, true)
		if err != nil {
			return err
		}

		return tx.RenameImageAlias(ctx, imgAliasID, req.Name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Rename image alias in authorizer.
	err = s.Authorizer.RenameImageAlias(r.Context(), projectName, name, req.Name)
	if err != nil {
		logger.Error("Failed to rename image alias in authorizer", logger.Ctx{"old_name": name, "new_name": req.Name, "project": projectName})
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ImageAliasRenamed.Event(req.Name, projectName, requestor, logger.Ctx{"old_name": name})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation GET /1.0/images/{fingerprint}/export?public images image_export_get_untrusted
//
//  Get the raw image file(s)
//
//  Download the raw image file(s) of a public image from the server.
//  If the image is in split format, a multipart http transfer occurs.
//
//  ---
//  produces:
//    - application/octet-stream
//    - multipart/form-data
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: secret
//      description: Secret token to retrieve a private image
//      type: string
//      example: RANDOM-STRING
//  responses:
//    "200":
//      description: Raw image data
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/images/{fingerprint}/export images image_export_get
//
//	Get the raw image file(s)
//
//	Download the raw image file(s) from the server.
//	If the image is in split format, a multipart http transfer occurs.
//
//	---
//	produces:
//	  - application/octet-stream
//	  - multipart/form-data
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Raw image data
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageExport(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	var imgInfo *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the image (expand the fingerprint).
		_, imgInfo, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Access control.
	var userCanViewImage bool
	err = s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectImage(projectName, imgInfo.Fingerprint), auth.EntitlementCanView)
	if err == nil {
		userCanViewImage = true
	} else if !api.StatusErrorCheck(err, http.StatusForbidden) {
		return response.SmartError(err)
	}

	public := d.checkTrustedClient(r) != nil || !userCanViewImage
	secret := r.FormValue("secret")

	if r.RemoteAddr == "@dev_incus" {
		// /dev/incus API requires exact match
		if imgInfo.Fingerprint != fingerprint {
			return response.NotFound(fmt.Errorf("Image %q not found", fingerprint))
		}

		if !imgInfo.Public && !imgInfo.Cached {
			return response.NotFound(fmt.Errorf("Image %q not found", fingerprint))
		}
	} else {
		op, err := imageValidSecret(s, r, projectName, imgInfo.Fingerprint, secret)
		if err != nil {
			return response.SmartError(err)
		}

		if !imgInfo.Public && public && op == nil {
			return response.NotFound(fmt.Errorf("Image %q not found", imgInfo.Fingerprint))
		}
	}

	var address string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if the image is only available on another node.
		address, err = tx.LocateImage(ctx, imgInfo.Fingerprint)

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	if address != "" {
		// Forward the request to the other node
		client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
		if err != nil {
			return response.SmartError(err)
		}

		return response.ForwardedResponse(client, r)
	}

	imagePath := internalUtil.VarPath("images", imgInfo.Fingerprint)
	rootfsPath := imagePath + ".rootfs"

	_, ext, _, err := archive.DetectCompression(imagePath)
	if err != nil {
		ext = ""
	}

	filename := fmt.Sprintf("%s%s", imgInfo.Fingerprint, ext)

	if util.PathExists(rootfsPath) {
		files := make([]response.FileResponseEntry, 2)

		files[0].Identifier = "metadata"
		files[0].Path = imagePath
		files[0].Filename = "meta-" + filename

		// Recompute the extension for the root filesystem, it may use a different
		// compression algorithm than the metadata.
		_, ext, _, err = archive.DetectCompression(rootfsPath)
		if err != nil {
			ext = ""
		}

		filename = fmt.Sprintf("%s%s", imgInfo.Fingerprint, ext)

		if imgInfo.Type == "virtual-machine" {
			files[1].Identifier = "rootfs.img"
		} else {
			files[1].Identifier = "rootfs"
		}

		files[1].Path = rootfsPath
		files[1].Filename = filename

		return response.FileResponse(r, files, nil)
	}

	files := make([]response.FileResponseEntry, 1)
	files[0].Identifier = filename
	files[0].Path = imagePath
	files[0].Filename = filename

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(projectName, lifecycle.ImageRetrieved.Event(imgInfo.Fingerprint, projectName, requestor, nil))

	return response.FileResponse(r, files, nil)
}

// swagger:operation POST /1.0/images/{fingerprint}/export images images_export_post
//
//	Make the server push the image to a remote server
//
//	Gets the server to connect to a remote server and push the image to it.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: image
//	    description: Image push request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageExportPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageExportPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if the image exists
		_, _, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	req := api.ImageExportPost{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		response.SmartError(err)
	}

	// Connect to the target and push the image
	args := &incus.ConnectionArgs{
		TLSServerCert: req.Certificate,
		UserAgent:     version.UserAgent,
		Proxy:         s.Proxy,
		CachePath:     s.OS.CacheDir,
		CacheExpiry:   time.Hour,
	}

	// Setup client
	remote, err := incus.ConnectIncus(req.Target, args)
	if err != nil {
		return response.SmartError(err)
	}

	var imageCreateOp incus.Operation

	run := func(op *operations.Operation) error {
		createArgs := &incus.ImageCreateArgs{}
		imageMetaPath := internalUtil.VarPath("images", fingerprint)
		imageRootfsPath := internalUtil.VarPath("images", fingerprint+".rootfs")

		metaFile, err := os.Open(imageMetaPath)
		if err != nil {
			return err
		}

		defer func() { _ = metaFile.Close() }()

		createArgs.MetaFile = metaFile
		createArgs.MetaName = filepath.Base(imageMetaPath)

		if util.PathExists(imageRootfsPath) {
			rootfsFile, err := os.Open(imageRootfsPath)
			if err != nil {
				return err
			}

			defer func() { _ = rootfsFile.Close() }()

			createArgs.RootfsFile = rootfsFile
			createArgs.RootfsName = filepath.Base(imageRootfsPath)
		}

		image := api.ImagesPost{
			Filename: createArgs.MetaName,
			Source: &api.ImagesPostSource{
				Fingerprint: fingerprint,
				Secret:      req.Secret,
				Mode:        "push",
			},
			ImagePut: api.ImagePut{
				Profiles: req.Profiles,
			},
		}

		if req.Project != "" {
			remote = remote.UseProject(req.Project)
		}

		imageCreateOp, err = remote.CreateImage(image, createArgs)
		if err != nil {
			return err
		}

		opAPI := imageCreateOp.Get()

		var secret string

		val, ok := opAPI.Metadata["secret"]
		if ok {
			secret = val.(string)
		}

		opWaitAPI, _, err := remote.GetOperationWaitSecret(opAPI.ID, secret, -1)
		if err != nil {
			return err
		}

		if opWaitAPI.StatusCode != api.Success {
			return fmt.Errorf("Failed operation %q: %q", opWaitAPI.Status, opWaitAPI.Err)
		}

		s.Events.SendLifecycle(projectName, lifecycle.ImageRetrieved.Event(fingerprint, projectName, op.Requestor(), logger.Ctx{"target": req.Target}))

		return nil
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.ImageDownload, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/images/{fingerprint}/secret images images_secret_post
//
//	Generate secret for retrieval of the image by an untrusted client
//
//	This generates a background operation including a secret one time key
//	in its metadata which can be used to fetch this image from an untrusted
//	client.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageSecret(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	var imgInfo *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, imgInfo, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	return createTokenResponse(s, r, projectName, imgInfo.Fingerprint, nil)
}

func imageImportFromNode(imagesDir string, client incus.InstanceServer, fingerprint string) error {
	// Prepare the temp files
	buildDir, err := os.MkdirTemp(imagesDir, "incus_build_")
	if err != nil {
		return fmt.Errorf("failed to create temporary directory for download: %w", err)
	}

	defer func() { _ = os.RemoveAll(buildDir) }()

	metaFile, err := os.CreateTemp(buildDir, "incus_tar_")
	if err != nil {
		return err
	}

	defer func() { _ = metaFile.Close() }()

	rootfsFile, err := os.CreateTemp(buildDir, "incus_tar_")
	if err != nil {
		return err
	}

	defer func() { _ = rootfsFile.Close() }()

	getReq := incus.ImageFileRequest{
		MetaFile:   io.WriteSeeker(metaFile),
		RootfsFile: io.WriteSeeker(rootfsFile),
	}

	getResp, err := client.GetImageFile(fingerprint, getReq)
	if err != nil {
		return err
	}

	// Truncate down to size
	if getResp.RootfsSize > 0 {
		err = rootfsFile.Truncate(getResp.RootfsSize)
		if err != nil {
			return err
		}
	}

	err = metaFile.Truncate(getResp.MetaSize)
	if err != nil {
		return err
	}

	if getResp.RootfsSize == 0 {
		// This is a unified image.
		rootfsPath := filepath.Join(imagesDir, fingerprint)
		err := internalUtil.FileMove(metaFile.Name(), rootfsPath)
		if err != nil {
			return err
		}
	} else {
		// This is a split image.
		metaPath := filepath.Join(imagesDir, fingerprint)
		rootfsPath := filepath.Join(imagesDir, fingerprint+".rootfs")

		err := internalUtil.FileMove(metaFile.Name(), metaPath)
		if err != nil {
			return nil
		}

		err = internalUtil.FileMove(rootfsFile.Name(), rootfsPath)
		if err != nil {
			return nil
		}
	}

	return nil
}

// swagger:operation POST /1.0/images/{fingerprint}/refresh images images_refresh_post
//
//	Refresh an image
//
//	This causes the server to check the image source server for an updated
//	version of the image and if available to refresh the local copy with the
//	new version.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func imageRefresh(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	fingerprint, err := url.PathUnescape(mux.Vars(r)["fingerprint"])
	if err != nil {
		return response.SmartError(err)
	}

	var imageID int
	var imageInfo *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		imageID, imageInfo, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &projectName})

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Begin background operation
	run := func(op *operations.Operation) error {
		var nodes []string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			nodes, err = tx.GetNodesWithImageAndAutoUpdate(ctx, fingerprint, true)

			return err
		})
		if err != nil {
			return fmt.Errorf("Error getting cluster members for refreshing image %q in project %q: %w", fingerprint, projectName, err)
		}

		newImage, err := autoUpdateImage(s.ShutdownCtx, s, op, imageID, imageInfo, projectName, true)
		if err != nil {
			return fmt.Errorf("Failed to update image %q in project %q: %w", fingerprint, projectName, err)
		}

		if newImage != nil {
			if len(nodes) > 1 {
				err := distributeImage(s.ShutdownCtx, s, nodes, fingerprint, newImage)
				if err != nil {
					return fmt.Errorf("Failed to distribute new image %q: %w", newImage.Fingerprint, err)
				}
			}

			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				// Remove the database entry for the image after distributing to cluster members.
				return tx.DeleteImage(ctx, imageID)
			})
			if err != nil {
				logger.Error("Error deleting old image from database", logger.Ctx{"err": err, "fingerprint": fingerprint, "ID": imageID})
			}
		}

		return err
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.ImageRefresh, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func autoSyncImagesTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		// In order to only have one task operation executed per image when syncing the images
		// across the cluster, only leader node can launch the task, no others.
		localClusterAddress := s.LocalConfig.ClusterAddress()

		leader, err := d.gateway.LeaderAddress()
		if err != nil {
			if errors.Is(err, cluster.ErrNodeIsNotClustered) {
				return // No error if not clustered.
			}

			logger.Error("Failed to get leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if localClusterAddress != leader {
			logger.Debug("Skipping image synchronization task since we're not leader")
			return
		}

		opRun := func(op *operations.Operation) error {
			return autoSyncImages(ctx, s)
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ImagesSynchronize, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating image synchronization operation", logger.Ctx{"err": err})
			return
		}

		logger.Debug("Acquiring image task lock")
		imageTaskMu.Lock()
		defer imageTaskMu.Unlock()
		logger.Debug("Acquired image task lock")

		logger.Info("Synchronizing images across the cluster")
		err = op.Start()
		if err != nil {
			logger.Error("Failed starting image synchronization operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed synchronizing images", logger.Ctx{"err": err})
			return
		}

		logger.Info("Done synchronizing images across the cluster")
	}

	return f, task.Hourly()
}

func autoSyncImages(ctx context.Context, s *state.State) error {
	var imageProjectInfo map[string][]string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		// Get all images.
		imageProjectInfo, err = tx.GetImages(ctx)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to query image fingerprints: %w", err)
	}

	for fingerprint, projects := range imageProjectInfo {
		ch := make(chan error)
		go func() {
			err := imageSyncBetweenNodes(s, nil, projects[0], fingerprint)
			if err != nil {
				logger.Error("Failed to synchronize images", logger.Ctx{"err": err, "fingerprint": fingerprint})
			}

			ch <- nil
		}()

		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}

	return nil
}

func imageSyncBetweenNodes(s *state.State, r *http.Request, project string, fingerprint string) error {
	logger.Info("Syncing image to members started", logger.Ctx{"fingerprint": fingerprint, "project": project})
	defer logger.Info("Syncing image to members finished", logger.Ctx{"fingerprint": fingerprint, "project": project})

	var desiredSyncNodeCount int64
	var syncNodeAddresses []string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		desiredSyncNodeCount = s.GlobalConfig.ImagesMinimalReplica()

		// -1 means that we want to replicate the image on all nodes
		if desiredSyncNodeCount == -1 {
			nodesCount, err := tx.GetNodesCount(ctx)
			if err != nil {
				return fmt.Errorf("Failed to get the number of nodes: %w", err)
			}

			desiredSyncNodeCount = int64(nodesCount)
		}

		var err error

		// Check how many nodes already have this image
		syncNodeAddresses, err = tx.GetNodesWithImage(ctx, fingerprint)
		if err != nil {
			return fmt.Errorf("Failed to get nodes for the image synchronization: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// If none of the nodes have the image, there's nothing to sync.
	if len(syncNodeAddresses) == 0 {
		logger.Info("No members have image, nothing to do", logger.Ctx{"fingerprint": fingerprint, "project": project})
		return nil
	}

	nodeCount := desiredSyncNodeCount - int64(len(syncNodeAddresses))
	if nodeCount <= 0 {
		logger.Info("Sufficient members have image", logger.Ctx{"fingerprint": fingerprint, "project": project, "desiredSyncCount": desiredSyncNodeCount, "syncedCount": len(syncNodeAddresses)})
		return nil
	}

	// Pick a random node from that slice as the source.
	syncNodeAddress := syncNodeAddresses[rand.Intn(len(syncNodeAddresses))]

	source, err := cluster.Connect(syncNodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
	if err != nil {
		return fmt.Errorf("Failed to connect to source node for image synchronization: %w", err)
	}

	source = source.UseProject(project)

	var image *api.Image

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the image.
		_, image, err = tx.GetImage(ctx, fingerprint, dbCluster.ImageFilter{Project: &project})

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to get image: %w", err)
	}

	// Populate the copy arguments with properties from the source image.
	args := incus.ImageCopyArgs{
		Type:   image.Type,
		Public: image.Public,
	}

	// Replicate on as many nodes as needed.
	for i := 0; i < int(nodeCount); i++ {
		var addresses []string

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Get a list of nodes that do not have the image.
			addresses, err = tx.GetNodesWithoutImage(ctx, fingerprint)

			return err
		})
		if err != nil {
			return fmt.Errorf("Failed to get nodes for the image synchronization: %w", err)
		}

		if len(addresses) <= 0 {
			logger.Info("All members have image", logger.Ctx{"fingerprint": fingerprint, "project": project})
			return nil
		}

		// Pick a random node from that slice as the target.
		targetNodeAddress := addresses[rand.Intn(len(addresses))]

		client, err := cluster.Connect(targetNodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
		if err != nil {
			return fmt.Errorf("Failed to connect node for image synchronization: %w", err)
		}

		// Select the right project.
		client = client.UseProject(project)

		// Copy the image to the target server.
		logger.Info("Copying image to member", logger.Ctx{"fingerprint": fingerprint, "address": targetNodeAddress, "project": project, "public": args.Public, "type": args.Type})
		op, err := client.CopyImage(source, *image, &args)
		if err != nil {
			return fmt.Errorf("Failed to copy image to %q: %w", targetNodeAddress, err)
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	return nil
}

func createTokenResponse(s *state.State, r *http.Request, projectName string, fingerprint string, metadata jmap.Map) response.Response {
	secret, err := internalUtil.RandomHexString(32)
	if err != nil {
		return response.InternalError(err)
	}

	meta := jmap.Map{}

	for k, v := range metadata {
		meta[k] = v
	}

	meta["secret"] = secret

	resources := map[string][]api.URL{}
	resources["images"] = []api.URL{*api.NewURL().Path(version.APIVersion, "images", fingerprint)}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassToken, operationtype.ImageToken, resources, meta, nil, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.ImageSecretCreated.Event(fingerprint, projectName, op.Requestor(), nil))

	return operations.OperationResponse(op)
}
