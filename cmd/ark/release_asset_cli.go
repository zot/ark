package main

// `ark _release-asset`: the Makefile's way to name release binaries. It answers
// with ReleaseAssetName, the same function `ark update` uses to choose its
// download, so a release and the updater cannot disagree about a name. Internal
// (leading `_`), filesystem-free, and never contacts a server.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	ucli "github.com/urfave/cli/v3"
	"github.com/zot/ark"
)

const releaseAssetTerseFlag = "terse"

// CRC: crc-Updater.md | R3547, R3548
func releaseAssetCommand() *ucli.Command {
	return &ucli.Command{
		Name:      "_release-asset",
		Usage:     "internal: the release asset name `ark update` downloads for a platform",
		ArgsUsage: "OS ARCH",
		Flags: []ucli.Flag{
			&ucli.BoolFlag{Name: releaseAssetTerseFlag, Usage: "print only the bare asset name"},
		},
		Action: releaseAssetAction,
	}
}

func releaseAssetAction(_ context.Context, c *ucli.Command) error {
	if c.Args().Len() != 2 {
		return fmt.Errorf("usage: ark _release-asset [--terse] OS ARCH (for example: windows amd64)")
	}
	return releaseAssetReport(os.Stdout, ark.Version, c.Args().Get(0), c.Args().Get(1), c.Bool(releaseAssetTerseFlag))
}

// releaseAssetReport writes the asset name for goos/goarch at version: baby-food
// bullets by default, the bare name when terse. R3548
func releaseAssetReport(w io.Writer, version, goos, goarch string, terse bool) error {
	name := releaseAssetName(version, goos, goarch)
	if terse {
		_, err := fmt.Fprintln(w, name)
		return err
	}
	_, err := fmt.Fprintf(w, "- asset: %s\n- version: %s\n- platform: %s/%s\n",
		name, strings.TrimPrefix(version, "v"), goos, goarch)
	return err
}
