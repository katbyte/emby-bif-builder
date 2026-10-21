// Package cli implements the ebb command line interface: the cobra commands, the settings, and how each command is run.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gookit/color"
	"github.com/katbyte/go-kt/cout"
	"github.com/katbyte/go-kt/version"
	"github.com/spf13/cobra"
)

func Make() (*cobra.Command, error) {
	root := &cobra.Command{
		Use:   "ebb [command]",
		Short: "ebb gives Emby its preview pictures: BIF files cut from Jellyfin's thumbnails, or built from the video",
		Long: `Gives Emby the pictures it shows while scrubbing through a video and for its chapters: a BIF
file beside each video. Where Jellyfin has made thumbnails of a video, the BIF is cut from
those, with nothing decoded; where it has not, the BIF can be built from the video with ffmpeg.

It reads Emby's and Jellyfin's databases to know what there is, and never writes to either.
It writes only BIFs, beside their videos, and removes only a BIF it is replacing.
Complete documentation is available at https://github.com/katbyte/emby-bif-builder`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Run \"ebb help\" for more information about available ebb commands.")
			return nil
		},
	}

	root.AddCommand(&cobra.Command{
		Use:           "version",
		Short:         "Print the version number of ebb",
		Long:          `Print the version number of ebb`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), versionString())
		},
	})

	root.AddCommand(statsCmd(), showCmd(), copyCmd(), buildCmd(), lookupCmd(), verifyCmd(), straysCmd())

	if err := configureFlags(root); err != nil {
		return nil, fmt.Errorf("unable to configure flags: %w", err)
	}
	// the settings file is read once the flags are parsed, since a flag can name it; before any command's own checks, which read what it holds
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		cout.Out, cout.Err = cmd.OutOrStdout(), cmd.ErrOrStderr() //nolint:reassign // what is said goes where the command's output does, as a test sets it
		GetFlags().Apply()
		// a list saved to a file, or read by another program, is kept free of the codes that colour a terminal
		if plain(cmd.OutOrStdout()) {
			color.Disable()
		}

		return nil
	}

	return root, nil
}

// plain says whether output written to w goes somewhere colours mean nothing: a file or another program rather than a terminal.
func plain(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return true
	}

	fi, err := f.Stat()

	return err != nil || fi.Mode()&os.ModeCharDevice == 0
}

// versionString is the name, version and commit the binary was built from, as the version command prints it. A build past a tag already names
// its commit in the version, so the commit is only added when it is not there.
func versionString() string {
	s := "ebb " + version.Version
	if version.GitCommit != "" && !strings.Contains(version.Version, version.GitCommit) {
		s += " (" + version.GitCommit + ")"
	}

	return s
}
