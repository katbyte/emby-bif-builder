# Changelog

## Unreleased

The first release: BIFs for Emby, cut from Jellyfin's thumbnails or built from the video.

- **`stats` counts what has a BIF and what does not**, library by library: films, shows, episodes and extras, those with a BIF and without, and what `copy` would make or replace. Then how wide the BIFs are, how they are named and who made them, and how wide Jellyfin's thumbnails are.
- **`copy` cuts a BIF from Jellyfin's thumbnails** for each video that has none, with nothing decoded: 640 wide for most videos, where Emby's own are 320.
- **`copy --upgrade` replaces narrower BIFs** with Jellyfin's wider thumbnails, and those that cannot be read or hold too few pictures, and removes every other BIF beside the video.
- **A BIF `ebb` cut is cut again** once Jellyfin has made its thumbnails again.
- **`build` makes a BIF from the video itself** with ffmpeg, for videos Jellyfin has not got to: on the processor or an NVIDIA card, from every frame or key frames alone.
- **`strays` finds the BIFs left behind** when their video was renamed or removed, and half-written ones a stopped run left; `--remove` removes them, reading each folder again first and recording each removal.
- **`lookup` says what one video has**, named by its path, a BIF beside it, a Jellyfin item ID or a trickplay folder, and what `copy` would do for it.
- **Nothing is written to either server's database**, and no lock is taken on one, so the servers never wait on it, even over a share.
- **Emby never reads half a BIF**: each is written under a hidden name and renamed into place once whole, and a BIF that appeared since its folder was read is left as it is.
- **Every BIF written, replaced or removed is recorded**, in a file only ever added to.
- **A dry run first**, and a limit on how many to do.
- **Runs where the servers do, or on another machine** that sees the media over a share, with `--media-paths` saying where.
- Ships as a Homebrew formula and ready-built programs, for Linux and macOS.
