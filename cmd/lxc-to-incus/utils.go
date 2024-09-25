package main

import (
	"fmt"
	"reflect"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/migration"
	"github.com/lxc/incus/v6/shared/api"
)

func transferRootfs(dst incus.InstanceServer, op incus.Operation, rootfs string, rsyncArgs string) error {
	opAPI := op.Get()

	// Connect to the websockets
	wsControl, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameControl].(string))
	if err != nil {
		return err
	}

	abort := func(err error) error {
		protoSendError(wsControl, err)
		return err
	}

	wsFs, err := op.GetWebsocket(opAPI.Metadata[api.SecretNameFilesystem].(string))
	if err != nil {
		return abort(err)
	}

	// Setup control struct
	fs := migration.MigrationFSType_RSYNC
	rsyncHasFeature := true
	offerHeader := migration.MigrationHeader{
		Fs: &fs,
		RsyncFeatures: &migration.RsyncFeatures{
			Xattrs:   &rsyncHasFeature,
			Delete:   &rsyncHasFeature,
			Compress: &rsyncHasFeature,
		},
	}

	err = migration.ProtoSend(wsControl, &offerHeader)
	if err != nil {
		return abort(err)
	}

	var respHeader migration.MigrationHeader
	err = migration.ProtoRecv(wsControl, &respHeader)
	if err != nil {
		return abort(err)
	}

	rsyncFeaturesOffered := offerHeader.GetRsyncFeaturesSlice()
	rsyncFeaturesResponse := respHeader.GetRsyncFeaturesSlice()

	if !reflect.DeepEqual(rsyncFeaturesOffered, rsyncFeaturesResponse) {
		return abort(fmt.Errorf("Offered rsync features (%v) differ from those in the migration response (%v)", rsyncFeaturesOffered, rsyncFeaturesResponse))
	}

	// Send the filesystem
	err = rsyncSend(wsFs, rootfs, rsyncArgs)
	if err != nil {
		return abort(err)
	}

	// Check the result
	msg := migration.MigrationControl{}
	err = migration.ProtoRecv(wsControl, &msg)
	if err != nil {
		_ = wsControl.Close()
		return err
	}

	if !msg.GetSuccess() {
		return fmt.Errorf(msg.GetMessage())
	}

	return nil
}
