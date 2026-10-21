# Development

```bash
make            # format and build
make test       # the tests, under the race detector
make testacc    # the live tests: the built program, as a person runs it
make cover      # the tests with coverage
make check-all  # build, tests, live tests, every linter and the dependency check
make tools      # install the pinned dev tools into .tools/bin
```

Dependencies are vendored. The dev tools are pinned in `.tools/go.mod` and the makefile. The tests that build from a video need ffmpeg; without it they are skipped, and the live tests fail.

## Where things are

| Path | What it does |
|---|---|
| `lib/bif` | writes and reads a BIF, and the note `ebb` puts in its first picture |
| `lib/trickplay` | where Jellyfin's sheets are, and cutting each thumbnail from them |
| `lib/readonly` | opens a server's database so nothing can be written to it and no lock is taken |
| `lib/emby` | Emby's libraries and videos, from its database |
| `lib/jellyfin` | Jellyfin's videos and their thumbnails, from its database |
| `lib/paths` | where a server's path is found here |
| `lib/inventory` | what each video has: whether it is there, its BIFs, Jellyfin's thumbnails |
| `lib/plan` | what to do for each video, and why |
| `lib/place` | puts a BIF in place, removes what it replaces, and keeps the record |
| `lib/ffmpeg` | takes the pictures from a video |
| `lib/servertest` | a made-up Emby and Jellyfin for the tests |
| `cli` | the commands and settings |
| `acceptance` | the live tests of the built program |

The logger, the console output and the running of jobs a few at a time are shared with the other tools, from [go-kt](https://github.com/katbyte/go-kt).

## The tests

`make test` needs neither server.

- Most tests lay out a made-up Emby and Jellyfin with `lib/servertest`: the two databases, holding the tables and columns `ebb` reads, a media folder, Jellyfin's sheets, and BIFs as Emby makes them.
- The servers see the videos under `/mnt/video`, and the test finds them in its own folder, so every test crosses from one to the other as `ebb` does on another machine.
- Each of Jellyfin's thumbnails is painted a shade of its own, so a BIF cut from the wrong place, or in the wrong order, fails.
- The videos ffmpeg is tested on brighten by the second, so a picture taken at the wrong time fails. That is how ffmpeg's own rounding was found to take each picture five seconds late, and is told to round up.
- The read-only database is checked to refuse writes and to leave its file, and the folder it is in, exactly as they were.

## The live tests

`make testacc` builds the program and runs it, with nothing of the environment of whoever runs it. It needs ffmpeg and takes a few seconds.

- It is given its settings from a settings file and from the environment.
- A dry run changes nothing; `copy` makes a BIF; `copy --upgrade` replaces Emby's narrow one and removes an old bare one beside it; a run after finds nothing to do; the record holds each change.
- `build` makes a BIF from a video Jellyfin has not got to.
- A run with nothing set ends badly, naming the settings it needs.

## What the tests cannot reach

- **Emby and Jellyfin themselves.** The databases are made-up copies of the parts `ebb` reads, laid out as Emby 4.11 and Jellyfin 12.2 lay theirs out. A server that changes them is only seen by running against it.
- **Emby showing the BIFs.** BIFs cut from Jellyfin's sheets were checked by hand in Emby's web player and Android app, for chapters and for scrubbing.
- **A video card.** What ffmpeg is told for `--hwaccel cuda` is checked; ffmpeg decoding on one is not.
