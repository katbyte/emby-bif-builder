// Package main implements ebb, which gives Emby the preview pictures it shows while scrubbing and for chapters: BIF files beside each video, cut
// from Jellyfin's thumbnails where Jellyfin has made them, or built from the video where it has not.
package main

import (
	"os"

	"github.com/gookit/color"
	"github.com/katbyte/emby-bif-builder/cli"
	"github.com/katbyte/go-kt/clog"
)

func main() {
	// the log level comes from EMBY_BIF_BUILDER_LOG; read it once here, before anything logs
	clog.SetLevelFromEnv("EMBY_BIF_BUILDER_LOG")

	cmd, err := cli.Make()
	if err != nil {
		clog.Log.Error(color.Sprintf("<red>ebb: building cmd</> %v", err))

		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		clog.Log.Error(color.Sprintf("<red>ebb:</> %v", err))

		os.Exit(1)
	}

	os.Exit(0)
}
