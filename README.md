# emby-bif-builder

[![GitHub release](https://img.shields.io/github/v/release/katbyte/emby-bif-builder?color=blueviolet)](https://github.com/katbyte/emby-bif-builder/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/katbyte/emby-bif-builder?color=00ADD8)](https://github.com/katbyte/emby-bif-builder/blob/main/go.mod)
[![License](https://img.shields.io/github/license/katbyte/emby-bif-builder?color=blue)](https://github.com/katbyte/emby-bif-builder/blob/main/LICENSE)
![build](https://github.com/katbyte/emby-bif-builder/actions/workflows/build.yaml/badge.svg)
![tests](https://github.com/katbyte/emby-bif-builder/actions/workflows/pr-tests.yaml/badge.svg)
![acceptance](https://github.com/katbyte/emby-bif-builder/actions/workflows/pr-acceptance.yaml/badge.svg)
![lint](https://github.com/katbyte/emby-bif-builder/actions/workflows/pr-golangci-lint.yaml/badge.svg)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/katbyte/emby-bif-builder/badges/coverage.json)](https://github.com/katbyte/emby-bif-builder/actions/workflows/coverage.yaml)

`ebb` gives Emby the pictures it shows while you scrub through a video and for its chapters: a BIF file beside each video. It also checks the BIFs already there, for ones that are broken or too short for their video, and finds ones left behind when their video was renamed or removed.

Where Jellyfin has already made thumbnails of a video, the BIF is cut from those, with no video decoded. Where it has not, the BIF can be built from the video with ffmpeg. Either way the pictures are 640 wide, where Emby's own are 320, which looks soft on a phone and blurry on a big screen.

**Colour means the same thing everywhere**: green is done or as it should be, yellow is something to do, red is wrong or failed, gray needs nothing, and cyan is what to look at. It is left out when the output is not a terminal.

**How it behaves.** It reads Emby's and Jellyfin's databases to know what there is, and never writes to either, nor takes a lock on them. It writes only BIFs, beside their videos, and removes only a BIF it is replacing. Each BIF is written under a hidden name and renamed into place once whole, so Emby never reads half of one. Every file it writes or removes goes in a record.

## Install

```bash
brew install katbyte/tap/emby-bif-builder               # or
go install github.com/katbyte/emby-bif-builder@latest   # which names it emby-bif-builder: make install names it ebb
```

or a ready-built program from the [releases](https://github.com/katbyte/emby-bif-builder/releases/latest), for Linux and macOS.

## Quick start

Point it at the two databases and Jellyfin's trickplay folder, in `~/.emby-bif-builder`:

```env
EMBY_DB_PATH=/home/docker/config/emby/data/library.db
JF_DB_PATH=/home/docker/config/jellyfin/data/jellyfin.db
JF_TRICKPLAY_PATH=/home/docker/config/jellyfin/data/trickplay
```

Then:

```bash
ebb stats                     # what has a BIF, what does not, and what Jellyfin has, library by library
ebb show not-found            # the videos Emby has whose file is not there
ebb copy --dry-run            # what copy would do, doing nothing
ebb copy                      # a BIF for every video Jellyfin has thumbnails of and that has none
ebb copy --upgrade            # and replace Emby's 320-wide ones with Jellyfin's 640-wide thumbnails
ebb build --keyframes         # a BIF from the video itself, for videos Jellyfin has not got to
ebb lookup <video, BIF or Jellyfin item ID>   # what one video has, and what copy would do for it
ebb verify                    # BIFs that cannot be read, or whose pictures stop well before their video ends
ebb strays                    # BIFs left behind when their video was renamed or removed; --remove removes them
```

`--limit 10` does no more than ten, for a first try. `--libraries TV` looks at one library alone.

## What each command does

| Command | What it does |
|---|---|
| [`stats`](docs/commands.md#stats) | counts each library's films, shows, episodes and extras, those with a BIF and without, and what copy would do; then how wide the BIFs are, how they are named and who made them, and how wide Jellyfin's thumbnails are |
| [`show not-found`](docs/commands.md#show-not-found) | lists the videos Emby has whose file is not there, and whether their folder is gone too |
| [`copy`](docs/commands.md#copy) | cuts a BIF from Jellyfin's thumbnail sheets for each video that has none; with `--upgrade`, also replaces BIFs narrower than Jellyfin's thumbnails, and removes the old ones |
| [`build`](docs/commands.md#build) | builds a BIF from the video with ffmpeg, for videos Jellyfin has no thumbnails of |
| [`lookup`](docs/commands.md#lookup) | says what a video, a BIF beside one, a Jellyfin item ID or a trickplay folder is, and what each server has for it |
| [`verify`](docs/commands.md#verify) | lists the BIFs that cannot be read, or whose pictures stop well before their video ends or run well past it, and which run replaces each; with `--pictures`, reads every picture in them |
| [`strays`](docs/commands.md#strays) | lists the BIFs whose video is no longer beside them, left when it was renamed or removed; with `--remove`, removes them |

Films and episodes get BIFs. Trailers, theme videos and the other extras only do with `--extras`.

## Settings

All can be a flag, an environment variable, or a line in a settings file.

| Variable | Flag | Default | What it sets |
|---|---|---|---|
| `EMBY_BIF_BUILDER_EMBY_DB_PATH` | `--emby-db-path` | | Emby's `library.db`, for its libraries and videos |
| `EMBY_BIF_BUILDER_JF_DB_PATH` | `--jf-db-path` | | Jellyfin's `jellyfin.db`, for the thumbnails it has made |
| `EMBY_BIF_BUILDER_JF_TRICKPLAY_PATH` | `--jf-trickplay-path` | | Jellyfin's trickplay folder, where its thumbnail sheets are |
| `EMBY_BIF_BUILDER_MEDIA_PATHS` | `--media-paths` | none | where the videos are found when that is not where the servers see them, as `/mnt/video=/Volumes/video` |
| `EMBY_BIF_BUILDER_LIBRARIES` | `--libraries` | all | the only libraries to look at, by name: `TV,Movies` |
| `EMBY_BIF_BUILDER_EXTRAS` | `--extras` | `false` | give trailers and the other extras BIFs too |
| `EMBY_BIF_BUILDER_NAME` | `--name` | `emby` | [what the BIFs written are called](#what-the-bifs-are-called) |
| `EMBY_BIF_BUILDER_RECORD` | `--record` | `~/.emby-bif-builder-changes.tsv` | the record of every BIF written, replaced or removed; empty for none |
| `EMBY_BIF_BUILDER_WORKERS` | `--workers` | `8` | how many folders are read, and BIFs cut, at once |
| `EMBY_BIF_BUILDER_BUILD_WORKERS` | `--build-workers` | `1` | how many videos are decoded at once |
| `EMBY_BIF_BUILDER_SIZE` | `--size` | `640` | how wide `build` makes the pictures, unless the video is narrower |
| `EMBY_BIF_BUILDER_FFMPEG` | `--ffmpeg` | `ffmpeg` | the ffmpeg `build` runs |
| `EMBY_BIF_BUILDER_HWACCEL` | `--hwaccel` | none | `cuda` to decode on an NVIDIA card |
| `EMBY_BIF_BUILDER_KEYFRAMES` | `--keyframes` | `false` | decode only key frames: many times faster, each picture the key frame at or before its time |
| `EMBY_BIF_BUILDER_LOG` | | `WARN` | log level: `INFO` or `DEBUG` say more |
| `EMBY_BIF_BUILDER_CONFIG` | `--config` | | the one settings file to read |

The settings file is `./.emby-bif-builder` or `~/.emby-bif-builder`. Its keys are the variable names without `EMBY_BIF_BUILDER_`. A flag or an environment variable wins over the file.

Each run also takes `--dry-run`, `--limit` and `--upgrade`, which are never settings, so a lasting setting cannot replace files nobody asked to.

## Where to run it

Anywhere that can read the two databases and Jellyfin's trickplay folder, and read and write the media.

- **On the media server's machine** it sees everything where the servers do, and a BIF it writes through the same share Emby reads the media from comes out just as Emby's own do: same owner, same permissions.
- **On another machine** that sees the media over a share, `--media-paths` says where: `/mnt/video=/Volumes/video`. A server folder mapped somewhere else gets a pair of its own, and the deepest folder wins.
- **A Mac cannot reach every name over a share.** A name whose accents are kept as separate characters, as some downloaded videos' are, is listed but cannot be opened, nor a BIF written beside it under its name. `ebb` leaves such a video and says so; run it where the files are for those.

The databases are opened read-only and as if they could not change, so no lock is taken and the servers never wait on `ebb`, even over a share. What a server wrote in its last few minutes and has not yet folded into its database file is seen on the next run.

`build` needs ffmpeg where it runs. It decodes on the processor unless told `--hwaccel cuda`, so a video card Jellyfin is busy with is left to it.

## What the BIFs are called

Emby finds a BIF beside a video whichever of these it is called, and shows what is inside. `--name` chooses:

- `emby`, the default: `<video>-320-10.bif`, the name Emby gives its own, whatever the width inside. Emby's own thumbnail task then counts the video as done and does not make a 320-wide one beside it.
- `size`: `<video>-640-10.bif`, the width inside and the seconds apart.
- `bare`: `<video>.bif`, as Roku players named them.

## How this is built

- **Tested against made-up servers.** The tests lay out an Emby and a Jellyfin database with the tables and columns of real ones, a media folder, Jellyfin's thumbnail sheets, and BIFs as Emby makes them.
- **Each picture is checked to be the right one.** Every thumbnail in a test is painted a shade of its own, so a BIF cut from the wrong place, or out of order, fails. A video made by ffmpeg brightens by the second, so a picture taken at the wrong time fails.
- **The program is tested as it ships.** Live tests run the built program from a settings file and from the environment, copy, upgrade, run again and build, and check what is left on disk and in the record.
- **What no test reaches.** Emby and Jellyfin themselves. BIFs cut from Jellyfin's sheets were checked by hand in Emby's web player and Android app, for chapters and for scrubbing.
- **Checks CI runs.** Unit tests under the race detector, and the live tests. golangci-lint, CodeQL, govulncheck and a scan of the built program. Linters for the workflows, shell scripts, YAML, markdown and spelling.

## More

- [Every command, and what it does to each video](docs/commands.md)
- [What a BIF is, and how Jellyfin keeps its thumbnails](docs/formats.md)
- [Development and tests](docs/development.md)
- [Contributing and releases](CONTRIBUTING.md)
