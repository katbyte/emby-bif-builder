# Commands

Every command reads Emby's libraries and videos from its database, then reads each folder the videos are in, once, for whether each video is there and what BIFs are beside it. Over a share that takes a few minutes for a few hundred thousand videos; a line on the way says how far it has got. `--quiet` leaves that out.

A BIF is any file beside a video named `<video>.bif` or `<video>-<width>-<seconds>.bif`, where `<video>` is the video's file name without its extension. What a BIF holds is read from the file itself, not its name: a `-320-10.bif` can hold pictures 640 wide.

## stats

Counts, library by library:

| Column | What it counts |
|---|---|
| films, shows, episodes, extras | what Emby has in the library |
| with a BIF, without | the videos with one or more BIFs beside them, and those with none |
| not found | the videos Emby has whose file is not there: [`show not-found`](#show-not-found) lists them |
| Jellyfin has | the videos Jellyfin has made thumbnails of |
| to copy | the videos with no BIF that Jellyfin has thumbnails of: what `copy` would make |
| to upgrade | the videos whose BIF is narrower than Jellyfin's thumbnails, cannot be read, or holds less than half as many pictures: what `copy --upgrade` would replace |

Then the BIFs: how wide their pictures are, how they are named, who made them (Emby, or `ebb` from Jellyfin or from the video), how many cannot be read or hold a single picture, how many videos have more than one, and how many are [strays](#strays). [`verify`](#verify) lists the ones that are wrong. Then Jellyfin's thumbnails: how many videos have them, how wide, and how many are of videos Emby does not have.

Extras are counted in their column and left out of the rest unless `--extras` is given.

## show not-found

Lists each video Emby has whose file is not where Emby has it, a line each, with why:

- **its folder is there, the file is not**: most often a video renamed or removed since Emby last read the library.
- **its folder is gone too**, or **its folder cannot be read**, with what stopped it.

Each line also says what Emby has the video as, and which library. Then how many, library by library. These are what `stats` counts as not found, and what `copy` and `build` leave alone; once Emby reads the library again, it drops them or finds them where they went. Nothing is written.

## copy

For each video Jellyfin has made thumbnails of (`copy-from-jf` is the same command):

| The video has | `copy` | `copy --upgrade` |
|---|---|---|
| no BIF | makes one | makes one |
| a BIF narrower than Jellyfin's thumbnails | leaves it | replaces it |
| a BIF that cannot be read, or holds less than half as many pictures | leaves it | replaces it |
| a BIF `ebb` cut, and Jellyfin has made its thumbnails again since | cuts it again | cuts it again |
| a BIF as wide as Jellyfin's thumbnails | leaves it | leaves it |
| no file where Emby has it | leaves it | leaves it |

The BIF holds Jellyfin's thumbnails as they are: the same width (640 for most videos, the video's own for one narrower), the same number, as far apart. Each is cut from its sheet and kept as a JPEG of quality 90, as Jellyfin keeps the sheets. Nothing is decoded but the sheets.

When a BIF is replaced, every other BIF beside the video is removed once the new one is in place, so Emby has only one to choose. A BIF written under the name of one already there takes its place in one step.

`ebb` knows a BIF it cut by a comment in its first picture naming the set of thumbnails and when their sheets were written, which anything showing the picture passes over. That is how it tells when Jellyfin has made them again.

## build

For each video Jellyfin has no thumbnails of, and that has no BIF, ffmpeg decodes the video and takes a picture every 10 seconds, or as often as the video's library is set to in Emby. Picture `k` is the frame at or before `k` × 10 seconds in, as Emby and Jellyfin count them. The pictures are `--size` wide, 640, unless the video is narrower as shown; a video whose pixels are not square is shown at its proper shape.

- `--keyframes` decodes only the key frames, which is many times faster. Each picture is then the key frame at or before its time, which can be a few seconds early.
- `--hwaccel cuda` decodes on an NVIDIA card, with an ffmpeg built to. A card already decoding for Jellyfin's own thumbnails is shared, and each goes slower.
- `--build-workers` says how many videos are decoded at once, 1 unless set.

With `--upgrade`, a BIF narrower than `--size`, and narrower than the video itself as Emby read it, is replaced, as is one that cannot be read.

Without `--jf-db-path`, every video without a BIF is built from, Jellyfin's or not.

## lookup

Takes any number of things, each one of:

- a video, as the servers see it or as it is found here,
- a BIF beside a video,
- a Jellyfin item ID, with or without its dashes, in either case,
- a folder or sheet in Jellyfin's trickplay folder, which is named by the item ID.

For each it says what video it is and what that video has: where it is found, what Emby takes it for and which library holds it, each BIF beside it and what it holds, Jellyfin's item and thumbnails and how many of their sheets are there, and what `copy` and `copy --upgrade` would do for it.

## verify

Reads every BIF beside the videos and lists each that is wrong, with what is wrong with it:

| Listed as | The BIF |
|---|---|
| broken | cannot be read: it is not a BIF, it was cut short, its index is out of order, or its first picture cannot be shown |
| short | holds pictures that stop well before the video does: a single picture for an hour of video, say |
| long | holds pictures that run well past the video's end, as a BIF of another cut of the video would |
| unopened | is listed but cannot be opened from here: see below. It is not counted as wrong, as nothing is known of it |

How long each video is comes from Emby's database, and a line says how long Emby has the video as, since that can be what is wrong. A BIF may fall short of its video, or run past it, by a tenth of the video and a picture or two before it is wrong, as the last picture falls either side of the end and the length Emby read can be a little out. A single picture of a clip under twenty seconds is fine. A BIF of a video Emby has no length for is only checked for whether it can be read, and the totals say how many there were.

Each line says whether `copy --upgrade` or `build --upgrade` replaces the BIF. A line that says neither is one no run of `ebb` replaces: a BIF already as wide as Jellyfin's thumbnails or as the video, say.

Reading a BIF reads its start: the header, the index of where each picture is, and the first picture. `--pictures` reads the whole of each and every picture in it, which finds a picture past the first that cannot be shown, holds nothing, or is a size other than the first. That is all of every BIF over the share, so it takes a good deal longer.

Nothing is written. The run ends badly when any BIF is wrong.

A Mac over a share lists a file whose name has its accents kept as separate characters, as some downloaded videos' are, but cannot open it. Such a video's BIFs are listed as unopened, `stats` counts them apart from those that cannot be read, and `copy` and `build` leave the video, as a BIF written from the Mac would be named so Emby does not find it. Run `ebb` where the files are for those.

## strays

Lists each BIF none of whose names could be a video's beside it: one left when its video was renamed or removed, whoever made it. Also each half-written BIF a stopped run of `ebb` left behind, once it is an hour old, so one a run is writing now is never taken for one.

A video is beside a BIF when a file of a video's kind has the name the BIF is named from, a folder does (a disc's), or Emby has a video of that name there and it was found. A video Emby has not got to yet keeps its BIF. Only the folders Emby's videos are in are read: a folder whose every video is gone, and that Emby no longer has, is not.

`--remove` removes them. Each folder is read again just before its strays are removed, and a BIF whose video has come back since stays. Each removal goes in the record.

## What every run that writes does

- **It is safe to stop.** An interrupt stops it between BIFs. A BIF being written is under a hidden name, `.<name>.<random>.ebb-tmp`, and is removed.
- **It says what it is doing.** Each video is said as it starts: which of how many it is, what is being done and why, what its BIF is made from (Jellyfin's set of thumbnails and how many sheets, or the video and how ffmpeg is taking its pictures) and the BIF it is written to. Once made, a line says what the BIF holds, how long it took and about how long the rest will take, and a line for each old BIF removed. Then the totals, with why the rest were left. `--verbose` adds a line for each video left and why.
- **A failure stops nothing else.** It is said, counted, and the run ends badly once the rest are done.
- **It keeps a record.** A line for every BIF written, replaced or removed, with tabs between when, what, the file and why, added to `--record`. The record is only ever added to.
- **It tries before doing.** `--dry-run` says what would be done and does nothing. `--limit` stops after so many videos.
