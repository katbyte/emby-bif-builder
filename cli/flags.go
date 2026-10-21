package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/katbyte/emby-bif-builder/lib/ffmpeg"
	"github.com/katbyte/emby-bif-builder/lib/paths"
	"github.com/katbyte/emby-bif-builder/lib/plan"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/cout"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// The defaults. Eight folders are read at once, which a share answers as fast as it answers one, and BIFs are cut eight at a time, as each takes a
// processor. Videos are built from one at a time, since decoding is what a machine has least of.
const (
	defaultWorkers      = 8
	defaultBuildWorkers = 1
	defaultSize         = 640
	defaultName         = "size"
	defaultFFmpeg       = "ffmpeg"
	defaultRecord       = "~/.emby-bif-builder-changes.tsv"
)

type FlagData struct {
	EmbyDB       string `mapstructure:"emby-db-path"`
	JellyfinDB   string `mapstructure:"jf-db-path"`
	Trickplay    string `mapstructure:"jf-trickplay-path"`
	MediaPaths   string `mapstructure:"media-paths"`
	Libraries    string `mapstructure:"libraries"`
	Extras       bool   `mapstructure:"extras"`
	Name         string `mapstructure:"name"`
	Record       string `mapstructure:"record"`
	Workers      int    `mapstructure:"workers"`
	BuildWorkers int    `mapstructure:"build-workers"`
	Size         int    `mapstructure:"size"`
	FFmpeg       string `mapstructure:"ffmpeg"`
	HWAccel      string `mapstructure:"hwaccel"`
	KeyFrames    bool   `mapstructure:"keyframes"`
	cout.Flags   `mapstructure:",squash"`
}

// envNames binds each setting to its environment variable.
var envNames = map[string]string{
	"emby-db-path":      "EMBY_BIF_BUILDER_EMBY_DB_PATH",
	"jf-db-path":        "EMBY_BIF_BUILDER_JF_DB_PATH",
	"jf-trickplay-path": "EMBY_BIF_BUILDER_JF_TRICKPLAY_PATH",
	"media-paths":       "EMBY_BIF_BUILDER_MEDIA_PATHS",
	"libraries":         "EMBY_BIF_BUILDER_LIBRARIES",
	"extras":            "EMBY_BIF_BUILDER_EXTRAS",
	"name":              "EMBY_BIF_BUILDER_NAME",
	"record":            "EMBY_BIF_BUILDER_RECORD",
	"workers":           "EMBY_BIF_BUILDER_WORKERS",
	"build-workers":     "EMBY_BIF_BUILDER_BUILD_WORKERS",
	"size":              "EMBY_BIF_BUILDER_SIZE",
	"ffmpeg":            "EMBY_BIF_BUILDER_FFMPEG",
	"hwaccel":           "EMBY_BIF_BUILDER_HWACCEL",
	"keyframes":         "EMBY_BIF_BUILDER_KEYFRAMES",
}

// persistent is the flag set configureFlags bound, for the one setting that has to be read before viper is: where the settings file is.
var persistent *pflag.FlagSet

func configureFlags(root *cobra.Command) error {
	pflags := root.PersistentFlags()

	pflags.String("emby-db-path", "", "Emby's database, library.db in its data folder: read for its libraries and videos, and never written to")
	pflags.String("jf-db-path", "", "Jellyfin's database, jellyfin.db in its data folder: read for the thumbnails it has made, and never written to")
	pflags.String("jf-trickplay-path", "", "Jellyfin's trickplay folder, where its thumbnail sheets are")
	pflags.String("media-paths", "", "where the videos are found here when that is not where the servers see them, as server=here,server=here: /mnt/video=/Volumes/video")
	pflags.String("libraries", "", "the only libraries to look at, by name with a comma between: every one unless set")
	pflags.Bool("extras", false, "give trailers, theme videos and the other extras BIFs too, not just films and episodes")
	pflags.String("name", defaultName, "what to call the BIFs written: size (<video>-640-10.bif, the width inside and the seconds apart), emby (<video>-320-10.bif, whatever the width inside) or bare (<video>.bif)")
	pflags.String("record", defaultRecord, "the file every BIF written, replaced or removed is added to, a line each; empty keeps no record")
	pflags.Int("workers", defaultWorkers, "how many folders to read, and BIFs to cut from Jellyfin's thumbnails, at once")
	pflags.Int("build-workers", defaultBuildWorkers, "how many videos to build BIFs from at once")
	pflags.Int("size", defaultSize, "how wide to build BIFs from the video, unless the video is narrower")
	pflags.String("ffmpeg", defaultFFmpeg, "the ffmpeg to build BIFs from the video with")
	pflags.String("hwaccel", "", "decode the video on the video card when building: cuda for an NVIDIA one; on the processor unless set")
	pflags.Bool("keyframes", false, "build from the video's key frames alone, which is much faster: each picture is then the key frame at or before its time")
	pflags.BoolP("quiet", "q", false, "say only what was done and the totals")
	pflags.BoolP("verbose", "v", false, "also say what was left as it is, and why")
	pflags.String("config", "", "read settings from this file and no other, in place of ./.emby-bif-builder or ~/.emby-bif-builder (or export EMBY_BIF_BUILDER_CONFIG)")

	persistent = pflags

	for name, env := range envNames {
		if err := viper.BindPFlag(name, pflags.Lookup(name)); err != nil {
			return fmt.Errorf("error binding '%s' flag: %w", name, err)
		}

		if err := viper.BindEnv(name, env); err != nil {
			return fmt.Errorf("error binding '%s' to env '%s' : %w", name, env, err)
		}
	}
	for _, name := range []string{"quiet", "verbose"} {
		if err := viper.BindPFlag(name, pflags.Lookup(name)); err != nil {
			return fmt.Errorf("error binding '%s' flag: %w", name, err)
		}
	}

	return nil
}

// configEnv names the settings file from the environment, as --config does on the command line.
const configEnv = "EMBY_BIF_BUILDER_CONFIG"

// configPath is the settings file a run was told to read, "" for none: the flag's, then the environment's.
func configPath() string {
	path := os.Getenv(configEnv)
	if persistent != nil {
		if fl := persistent.Lookup("config"); fl != nil && fl.Changed {
			path = fl.Value.String()
		}
	}

	return home(path)
}

// home is the path with a leading ~/ made the home directory, for whatever starts the program without a shell to expand it.
func home(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if dir, err := os.UserHomeDir(); err == nil {
			return filepath.Join(dir, rest)
		}
	}

	return path
}

// loadConfig reads the settings file, once the flags are parsed: the one --config or EMBY_BIF_BUILDER_CONFIG names and no other, or else the first
// .emby-bif-builder found in the working directory and then in $HOME. A file that was named and cannot be read is an error; one merely absent from
// the usual places is not.
func loadConfig() error {
	viper.SetConfigType("env")
	if path := configPath(); path != "" {
		viper.SetConfigFile(path)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("reading the settings file %s (--config / %s): %w", path, configEnv, err)
		}
	} else {
		viper.SetConfigName(".emby-bif-builder")
		// viper reads the first file it finds, so the working directory comes first
		viper.AddConfigPath(".")
		if dir, err := os.UserHomeDir(); err == nil {
			viper.AddConfigPath(dir)
		}

		if err := viper.ReadInConfig(); err != nil {
			if _, ok := errors.AsType[viper.ConfigFileNotFoundError](err); !ok {
				clog.Log.Errorf("Error reading config file: %v", err)
			}
		}
	}

	// a settings file spells a setting the way the environment does, less the prefix (JF_DB_PATH for --jf-db-path), and viper only matches a key
	// spelled as the flag is. Carried across as defaults, they still lose to a flag or the environment.
	for name := range envNames {
		if alt := strings.ReplaceAll(name, "-", "_"); alt != name && viper.InConfig(alt) {
			viper.SetDefault(name, viper.Get(alt))
		}
	}

	return nil
}

// GetFlags returns the fully populated FlagData. It is read from viper rather than from the flags themselves, since viper is what lays the
// environment and the settings file under the command line.
func GetFlags() *FlagData {
	var f FlagData
	if err := viper.Unmarshal(&f); err != nil {
		clog.Log.Fatalf("failed to unmarshal configuration: %v", err)
	}

	return &f
}

// paths is where the videos are found, from --media-paths.
func (f *FlagData) paths() (paths.Map, error) {
	m, err := paths.Parse(f.MediaPaths)
	if err != nil {
		return nil, fmt.Errorf("--media-paths (%s): %w", envNames["media-paths"], err)
	}

	return m, nil
}

// libraries is the libraries to look at, from --libraries: none for all of them.
func (f *FlagData) libraries() []string {
	var names []string
	for name := range strings.SplitSeq(f.Libraries, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}

	return names
}

// naming is what to call the BIFs written, from --name.
func (f *FlagData) naming() (plan.Naming, error) {
	n, err := plan.ParseNaming(f.Name)
	if err != nil {
		return 0, fmt.Errorf("--name (%s): %w", envNames["name"], err)
	}

	return n, nil
}

// check says what is wrong with the settings every command shares: a number of workers below one, a width no BIF could have, a video card ebb
// does not know.
func (f *FlagData) check() error {
	switch {
	case f.Workers < 1:
		return fmt.Errorf("--workers (%s) is %d: it takes at least one", envNames["workers"], f.Workers)
	case f.BuildWorkers < 1:
		return fmt.Errorf("--build-workers (%s) is %d: it takes at least one", envNames["build-workers"], f.BuildWorkers)
	case f.Size < 16 || f.Size > 7680:
		return fmt.Errorf("--size (%s) is %d: a BIF's pictures are from 16 to 7680 wide", envNames["size"], f.Size)
	case !slices.Contains(ffmpeg.HWAccels, f.HWAccel):
		return fmt.Errorf("--hwaccel (%s) is %q: it is cuda, or unset to decode on the processor", envNames["hwaccel"], f.HWAccel)
	}

	return nil
}

// need says which settings a command needs and are not set, naming each with its variable.
func need(settings map[string]string) error {
	var missing []string
	for _, name := range slices.Sorted(maps.Keys(settings)) {
		if settings[name] == "" {
			missing = append(missing, fmt.Sprintf("--%s (%s)", name, envNames[name]))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("this needs %s set", strings.Join(missing, " and "))
	}

	return nil
}
