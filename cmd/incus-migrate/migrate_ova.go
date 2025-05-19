package main

import (
	"archive/tar"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/archive"
	"github.com/lxc/incus/v6/shared/ask"
	"github.com/lxc/incus/v6/shared/util"
)

// VHResourceType defines what kind of resource this is (e.g., CPU, memory).
type VHResourceType string

const (
	vhResourceTypeOther     VHResourceType = "1"
	vhResourceTypeProcessor VHResourceType = "3"
	vhResourceTypeMemory    VHResourceType = "4"
)

// Envelope represents the root of the OVF file.
// It typically wraps all metadata about the virtual appliance.
type Envelope struct {
	XMLName       xml.Name      `xml:"Envelope"`
	References    References    `xml:"References"`
	DiskSection   DiskSection   `xml:"DiskSection"`
	VirtualSystem VirtualSystem `xml:"VirtualSystem"`
}

// References lists all external files used by the OVF (e.g., VMDK files).
type References struct {
	Files []File `xml:"File"`
}

// File describes one file (usually a disk image) referenced by the OVF.
type File struct {
	ID   string `xml:"id,attr"`
	Href string `xml:"href,attr"`
	Size int64  `xml:"size,attr"`
}

// DiskSection contains one or more virtual disks definitions.
type DiskSection struct {
	Disks []Disk `xml:"Disk"`
}

// Disk describes a virtual disk (size, backing file, format).
type Disk struct {
	DiskID   string `xml:"diskId,attr"`
	FileRef  string `xml:"fileRef,attr"`
	Capacity string `xml:"capacity,attr"`
	Format   string `xml:"format,attr"`
}

// VirtualSystem defines the configuration of a single virtual machine.
type VirtualSystem struct {
	ID                     string                 `xml:"id,attr"`
	Name                   string                 `xml:"Name"`
	VirtualHardwareSection VirtualHardwareSection `xml:"VirtualHardwareSection"`
}

// VirtualHardwareSection lists all the hardware components for the VM.
type VirtualHardwareSection struct {
	Items []Item `xml:"Item"`
}

// Item contains individual hardware definitions (CPU, memory, disk, etc.).
type Item struct {
	Description     string `xml:"Description"`
	ElementName     string `xml:"ElementName"`
	InstanceID      string `xml:"InstanceID"`
	ResourceType    string `xml:"ResourceType"`
	VirtualQuantity string `xml:"VirtualQuantity"`
	Connection      string `xml:"Connection"`
	HostResource    string `xml:"HostResource"`
}

// OVAMigration handles the migration logic for an instance from .ova file.
type OVAMigration struct {
	*Migration

	flagRsyncArgs string
	instance      *InstanceMigration
	ovaPath       string
	references    map[string]string
}

// NewOVAMigration returns a new OVAMigration.
func NewOVAMigration(ctx context.Context, server incus.InstanceServer, asker ask.Asker, flagRsyncArgs string) Migrator {
	return &OVAMigration{
		Migration: &Migration{
			asker:         asker,
			ctx:           ctx,
			server:        server,
			migrationType: MigrationTypeVM,
		},
		instance:      NewInstanceMigration(ctx, server, asker, flagRsyncArgs, MigrationTypeVM).(*InstanceMigration),
		flagRsyncArgs: flagRsyncArgs,
		references:    map[string]string{},
	}
}

// gatherInfo collects information from the user about the instance to be created.
func (m *OVAMigration) gatherInfo() error {
	err := m.askOVAPath()
	if err != nil {
		return err
	}

	// Project
	err = m.askProject("Project to create the instance in [default=default]: ")
	if err != nil {
		return err
	}

	if m.project != "" {
		m.server = m.server.UseProject(m.project)
	}

	// Target
	err = m.askTarget()
	if err != nil {
		return err
	}

	m.server = m.server.UseTarget(m.target)

	// Pool
	pools, err := m.server.GetStoragePools()
	if err != nil {
		return err
	}

	poolNames := []string{}
	for _, p := range pools {
		poolNames = append(poolNames, p.Name)
	}

	for {
		poolName, err := m.asker.AskString("Name of the pool: ", "", nil)
		if err != nil {
			return err
		}

		if !slices.Contains(poolNames, poolName) {
			fmt.Printf("Pool %q doesn't exists\n", poolName)
			continue
		}

		m.pool = poolName
		break
	}

	m.instance.instanceArgs = api.InstancesPost{
		Source: api.InstanceSource{
			Type: "migration",
			Mode: "push",
		},
	}

	m.instance.instanceArgs.Config = map[string]string{}
	m.instance.instanceArgs.Devices = map[string]map[string]string{}
	m.instance.instanceArgs.Devices["root"] = map[string]string{
		"type": "disk",
		"pool": m.pool,
		"path": "/",
	}

	m.instance.instanceArgs.Type = api.InstanceTypeVM
	m.instance.project = m.project

	// Instance name
	instanceNames, err := m.server.GetInstanceNames(api.InstanceTypeAny)
	if err != nil {
		return err
	}

	for {
		instanceName, err := m.asker.AskString("Name of the new instance: ", "", nil)
		if err != nil {
			return err
		}

		if slices.Contains(instanceNames, instanceName) {
			fmt.Printf("Instance %q already exists\n", instanceName)
			continue
		}

		m.instance.instanceArgs.Name = instanceName
		break
	}

	err = m.instance.askUEFISupport()
	if err != nil {
		return err
	}

	err = m.readOVA()
	if err != nil {
		return err
	}

	return nil
}

// migrate performs the instance migration.
func (m *OVAMigration) migrate() error {
	if m.migrationType != MigrationTypeVM {
		return errors.New("Wrong migration type for migrate")
	}

	// Create the temporary directory to be used for the ova files.
	outputPath, err := os.MkdirTemp("", "incus-migrate_ova_")
	if err != nil {
		return err
	}

	defer func() { _ = os.RemoveAll(outputPath) }()

	err = m.unpackOVA(outputPath)
	if err != nil {
		return err
	}

	// Update source paths for the instance and additional disks.
	// Currently, only filenames are kept, as the full paths become known after unpacking the OVA.
	m.instance.sourcePath = filepath.Join(outputPath, m.instance.sourcePath)
	err = m.validateDiskFormat(m.instance.sourcePath)
	if err != nil {
		return err
	}

	for _, v := range m.instance.volumes {
		// If the disk doesn't come from an OVA file, leave the source path unchanged.
		if !m.isDiskFromOVA(v.sourcePath) {
			continue
		}

		v.sourcePath = filepath.Join(outputPath, v.sourcePath)
		err = m.validateDiskFormat(m.instance.sourcePath)
		if err != nil {
			return err
		}
	}

	return m.instance.migrate()
}

// renderObject renders the state of the instance.
func (m *OVAMigration) renderObject() error {
	return m.instance.renderObject()
}

// askOVAPath prompts the user to provide the path to the .ova file.
func (m *OVAMigration) askOVAPath() error {
	var err error

	m.ovaPath, err = m.asker.AskString("Provide .ova file path: ", "", func(s string) error {
		if !util.PathExists(s) {
			return errors.New("Path does not exist")
		}

		_, err := os.Stat(s)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// unpackOVA extracts the contents of the OVA file.
func (m *OVAMigration) unpackOVA(outPath string) error {
	file, err := os.Open(m.ovaPath)
	if err != nil {
		return err
	}

	defer file.Close()

	tarReader := tar.NewReader(file)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of archive
		}

		if err != nil {
			return err
		}

		outFile, err := os.Create(filepath.Join(outPath, header.Name))
		if err != nil {
			fmt.Println("Error creating file:", err)
			continue
		}

		_, err = io.Copy(outFile, tarReader)
		if err != nil {
			fmt.Println("Error extracting file:", err)
			outFile.Close()
			continue
		}

		outFile.Close()
	}

	return nil
}

// readOVA reads the contents of the OVA file and parses the embedded OVF file.
func (m *OVAMigration) readOVA() error {
	file, err := os.Open(m.ovaPath)
	if err != nil {
		return err
	}

	defer file.Close()

	tarReader := tar.NewReader(file)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of archive
		}

		if err != nil {
			return err
		}

		// Look for the manifest (xml) file
		if strings.HasSuffix(header.Name, ".ovf") {
			var env Envelope
			decoder := xml.NewDecoder(tarReader)
			err := decoder.Decode(&env)
			if err != nil {
				panic(err)
			}

			return m.readOVFData(env)
		}
	}

	return nil
}

// readOVFData parses the OVF file and extracts information from it.
func (m *OVAMigration) readOVFData(env Envelope) error {
	for _, f := range env.References.Files {
		m.references[f.ID] = f.Href
	}

	// Extract vCPUs and memory
	for _, item := range env.VirtualSystem.VirtualHardwareSection.Items {
		switch item.ResourceType {
		case string(vhResourceTypeProcessor):
			m.instance.instanceArgs.Config["limits.cpu"] = item.VirtualQuantity
		case string(vhResourceTypeMemory):
			m.instance.instanceArgs.Config["limits.memory"] = fmt.Sprintf("%sMB", item.VirtualQuantity)
		}
	}

	// Add disks
	for idx, disk := range env.DiskSection.Disks {
		if idx == 0 {
			m.instance.sourcePath = m.references[disk.FileRef]
			continue
		}

		err := m.addDisk(m.references[disk.FileRef], idx)
		if err != nil {
			return err
		}
	}

	return nil
}

// addDisk adds an additional disk to the VM instance.
func (m *OVAMigration) addDisk(diskFileName string, index int) error {
	volMigrator, ok := NewVolumeMigration(m.ctx, m.server, m.asker, m.flagRsyncArgs).(*VolumeMigration)
	if !ok {
		return errors.New("Migrator should be of type VolumeMigration")
	}

	diskName := fmt.Sprintf("%s-disk%d", m.instance.instanceArgs.Name, index)

	volMigrator.migrationType = MigrationTypeVolumeBlock
	volMigrator.project = m.project
	volMigrator.pool = m.pool
	volMigrator.sourcePath = diskFileName

	volMigrator.customVolumeArgs = api.StorageVolumesPost{
		ContentType: "block",
		Name:        diskName,
		Type:        "custom",
		Source: api.StorageVolumeSource{
			Type: "migration",
			Mode: "push",
		},
	}

	m.instance.instanceArgs.Devices[volMigrator.customVolumeArgs.Name] = map[string]string{
		"type":   "disk",
		"pool":   volMigrator.pool,
		"source": diskName,
	}

	m.instance.volumes = append(m.instance.volumes, volMigrator)

	return nil
}

// validateDiskFormat checks whether the provided disk format is supported.
func (m *OVAMigration) validateDiskFormat(path string) error {
	_, ext, _, _ := archive.DetectCompression(path)
	if ext != ".vmdk" {
		return fmt.Errorf("%s disk format not supported", ext)
	}

	return nil
}

// isDiskFromOVA verifies whether the disk originates from an OVA file.
func (m *OVAMigration) isDiskFromOVA(name string) bool {
	for _, v := range m.references {
		if v == name {
			return true
		}
	}

	return false
}
