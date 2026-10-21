# Changelog

## 0.1.0 (2026-10-20)

The first release: BIFs for Emby, cut from Jellyfin's thumbnails or built from the video.

- **`scan` reads everything once and says what it found**: each BIF that is broken, or whose pictures stop well before their video ends or run well past it, with the run that replaces it; each stray; each video Emby has whose file is not there. Then library by library the films, shows, episodes and extras, those with a BIF and without, and what `copy` would make or replace, and how wide the BIFs are, how they are named, who made them and how many are wrong. `--pictures` reads every picture in every BIF.
- **`copy` cuts a BIF from Jellyfin's thumbnails** for each video that has none, with nothing decoded: 640 wide for most videos, where Emby's own are 320.
- **`copy --upgrade` replaces narrower BIFs** with Jellyfin's wider thumbnails, and those that cannot be read or hold too few pictures, and removes every other BIF beside the video.
- **A BIF `ebb` cut is cut again** once Jellyfin has made its thumbnails again.
- **`build` makes a BIF from the video itself** with ffmpeg, for videos Jellyfin has not got to: on the processor or an NVIDIA card, from every frame or key frames alone.
- **A video a Mac cannot reach over a share is left, and said**: one whose name has its accents kept as separate characters is listed but cannot be opened, so its BIFs are not taken for broken, and no BIF is written beside it under a name Emby would not find. A file the share says is not there but its folder lists is not taken to be removed.
- **`clean` removes the strays**: BIFs left behind when their video was renamed or removed, and half-written ones a stopped run left, reading each folder again first and recording each removal.
- **`lookup` says what one video has**, named by its path, a BIF beside it, a Jellyfin item ID or a trickplay folder, and what `copy` would do for it.
- **Colour that means something**, the same in every command: green done or fine, yellow to do, red wrong or failed, gray nothing needed.
- **`copy` and `build` say each video as they go**: which of how many it is, what its BIF is made from and where it goes, then what was made and about how long the rest will take.
- **Progress goes from red to green**: the count and percentage of folders read, and the number of each video copied or built, change colour as the run goes.
- **Each step says what it is doing**: reading Emby's videos, then Jellyfin's thumbnails, then the folders, each with how many it found and how long it took.
- **Jellyfin's thumbnails are read in seconds over a share**, not a minute or two, from two of its indexes rather than every item.
- **A database read from a Mac over a share is read as it is now**: the Mac's copy of it in memory is dropped first, as the Mac is not told when the server changes it and would hand back parts of it as they were, which reads as a damaged database.
- **Nothing is written to either server's database**, and no lock is taken on one, so the servers never wait on it, even over a share.
- **Emby never reads half a BIF**: each is written under a hidden name and renamed into place once whole, and a BIF that appeared since its folder was read is left as it is.
- **Every BIF written, replaced or removed is recorded**, in a file only ever added to.
- **A dry run first**, and a limit on how many to do.
- **Runs where the servers do, or on another machine** that sees the media over a share, with `--media-paths` saying where.
- Ships as a Homebrew formula and ready-built programs, for Linux and macOS.
