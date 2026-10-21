# Commands

Every command reads Emby's libraries and videos from its database, then reads each folder the videos are in, once, for whether each video is there and what BIFs are beside it. Over a share that takes a few minutes for a few hundred thousand videos; a line on the way says how far it has got. `--quiet` leaves that out.

A BIF is any file beside a video named `<video>.bif` or `<video>-<width>-<seconds>.bif`, where `<video>` is the video's file name without its extension. What a BIF holds is read from the file itself, not its name: a `-320-10.bif` can hold pictures 640 wide.

## stats

Counts, library by library:

| Column | What it counts |
|---|---|
| films, shows, episodes, extras | what Emby has in the library |
| with a BIF, without | the videos with one or more BIFs beside them, and those with none |
| not found | the videos Emby has whose file is not there |
| Jellyfin has | the videos Jellyfin has made thumbnails of |
| to copy | the videos with no BIF that Jellyfin has thumbnails of: what `copy` would make |
| to upgrade | the videos whose BIF is narrower than Jellyfin's thumbnails, cannot be read, or holds less than half as many pictures: what `copy --upgrade` would replace |

Then the BIFs: how wide their pictures are, how they are named, who made them (Emby, or `ebb` from Jellyfin or from the video), how many cannot be read or hold a single picture, how many videos have more than one, and how many are [strays](#strays). Then Jellyfin's thumbnails: how many videos have them, how wide, and how many are of videos Emby does not have.

Extras are counted in their column and left out of the rest unless `--extras` is given.

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

## strays

Lists each BIF none of whose names could be a video's beside it: one left when its video was renamed or removed, whoever made it. Also each half-written BIF a stopped run of `ebb` left behind, once it is an hour old, so one a run is writing now is never taken for one.

A video is beside a BIF when a file of a video's kind has the name the BIF is named from, a folder does (a disc's), or Emby has a video of that name there and it was found. A video Emby has not got to yet keeps its BIF. Only the folders Emby's videos are in are read: a folder whose every video is gone, and that Emby no longer has, is not.

`--remove` removes them. Each folder is read again just before its strays are removed, and a BIF whose video has come back since stays. Each removal goes in the record.

## What every run that writes does

- **It is safe to stop.** An interrupt stops it between BIFs. A BIF being written is under a hidden name, `.<name>.<random>.ebb-tmp`, and is removed.
- **It says what it did.** A line for each BIF made, upgraded or cut again, and for each removed, then the totals, with why the rest were left. `--verbose` adds a line for each video left and why.
- **A failure stops nothing else.** It is said, counted, and the run ends badly once the rest are done.
- **It keeps a record.** A line for every BIF written, replaced or removed, with tabs between when, what, the file and why, added to `--record`. The record is only ever added to.
- **It tries before doing.** `--dry-run` says what would be done and does nothing. `--limit` stops after so many videos.
