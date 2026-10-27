package main

import (
	"fmt"
	"os"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	"github.com/lxc/incus/v7/internal/i18n"
	internalUtil "github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/revert"
)

// nearLiveMoveDeleteSnapshot removes a pre-copy snapshot.
func nearLiveMoveDeleteSnapshot(server incus.InstanceServer, instName string, snapName string, warn bool) {
	op, err := server.DeleteInstanceSnapshot(instName, snapName)
	if err == nil {
		err = op.Wait()
	}

	if err != nil && warn {
		fmt.Fprintf(os.Stderr, "%s "+i18n.G("Failed to delete pre-copy snapshot %q of instance %q: %s")+"\n", color.WarningPrefix, snapName, instName, err)
	}
}

// nearLiveMoveCopyRound performs a single copy of the instance onto the target server.
func (c *cmdCopy) nearLiveMoveCopyRound(srcServer incus.InstanceServer, dstServer incus.InstanceServer, entry *api.Instance, args *incus.InstanceCopyArgs, format string) error {
	op, err := dstServer.CopyInstance(srcServer, *entry, args)
	if err != nil {
		return err
	}

	progress := cli.ProgressRenderer{
		Format: format,
		Quiet:  c.global.flagQuiet,
	}

	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	err = cli.CancelableWait(op, &progress)
	if err != nil {
		progress.Done("")
		return err
	}

	progress.Done("")

	return nil
}

// nearLiveMoveInstance moves a running container to another server with minimal downtime.
func (c *cmdCopy) nearLiveMoveInstance(srcServer incus.InstanceServer, dstServer incus.InstanceServer, srcName string, dstName string, entry *api.Instance, args *incus.InstanceCopyArgs) error {
	reverter := revert.New()
	defer reverter.Fail()

	instUUID := entry.Config["volatile.uuid"]
	if instUUID == "" {
		var err error
		instUUID, err = internalUtil.RandomHexString(16)
		if err != nil {
			return err
		}
	}

	snapshotNames := []string{}

	// Pre-copy rounds, run while the instance keeps serving.
	for round := 1; round <= 2; round++ {
		snapName := fmt.Sprintf("move-of-%s-%d", instUUID, round)

		// Clear out anything an earlier attempt left behind, as the names are reused.
		nearLiveMoveDeleteSnapshot(srcServer, srcName, snapName, false)

		op, err := srcServer.CreateInstanceSnapshot(srcName, api.InstanceSnapshotsPost{Name: snapName})
		if err == nil {
			err = op.Wait()
		}

		if err != nil {
			return fmt.Errorf(i18n.G("Failed to create pre-copy snapshot %q: %w"), snapName, err)
		}

		reverter.Add(func() { nearLiveMoveDeleteSnapshot(srcServer, srcName, snapName, true) })

		snapshotNames = append(snapshotNames, snapName)

		// The first round creates the instance on the target, later rounds refresh it.
		args.Refresh = round > 1

		err = c.nearLiveMoveCopyRound(srcServer, dstServer, entry, args, i18n.G("Pre-copying instance: %s"))
		if err != nil {
			return err
		}

		// From now on a failure leaves a full copy behind on the target server.
		if round == 1 {
			reverter.Add(func() {
				op, err := dstServer.DeleteInstance(dstName)
				if err == nil {
					err = op.Wait()
				}

				if err != nil {
					fmt.Fprintf(os.Stderr, "%s "+i18n.G("Failed to delete partial copy of instance %q on the target server: %s")+"\n", color.WarningPrefix, dstName, err)
				}
			})
		}
	}

	// Stop the instance for the final transfer.
	op, err := srcServer.UpdateInstanceState(srcName, api.InstanceStatePut{Action: "stop", Timeout: -1}, "")
	if err == nil {
		err = op.Wait()
	}

	if err != nil {
		op, err = srcServer.UpdateInstanceState(srcName, api.InstanceStatePut{Action: "stop", Force: true}, "")
		if err == nil {
			err = op.Wait()
		}

		if err != nil {
			return fmt.Errorf(i18n.G("Failed to stop instance %q: %w"), srcName, err)
		}
	}

	reverter.Add(func() {
		op, err := srcServer.UpdateInstanceState(srcName, api.InstanceStatePut{Action: "start"}, "")
		if err == nil {
			err = op.Wait()
		}

		if err != nil {
			fmt.Fprintf(os.Stderr, "%s "+i18n.G("Failed to restart instance %q after a failed move: %s")+"\n", color.WarningPrefix, srcName, err)
		}
	})

	// Final transfer, carrying everything written since the last pre-copy snapshot.
	args.Refresh = true

	err = c.nearLiveMoveCopyRound(srcServer, dstServer, entry, args, i18n.G("Transferring instance: %s"))
	if err != nil {
		return err
	}

	// The instance now lives on the target server, so don't restart the source on failure.
	reverter.Success()

	// Drop the pre-copy snapshots from both ends.
	for _, snapName := range snapshotNames {
		nearLiveMoveDeleteSnapshot(dstServer, dstName, snapName, true)
		nearLiveMoveDeleteSnapshot(srcServer, srcName, snapName, false)
	}

	// Start the instance on the target server.
	op, err = dstServer.UpdateInstanceState(dstName, api.InstanceStatePut{Action: "start"}, "")
	if err == nil {
		err = op.Wait()
	}

	if err != nil {
		return fmt.Errorf(i18n.G("Failed to start instance %q on the target server: %w"), dstName, err)
	}

	return nil
}
