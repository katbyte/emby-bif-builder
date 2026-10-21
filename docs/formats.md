# What ebb reads and writes

## A BIF

Roku's format, which Emby reads for its scrubbing and chapter pictures. Every number is little-endian.

| Bytes | What they hold |
|---|---|
| 0-7 | `89 42 49 46 0d 0a 1a 0a` |
| 8-11 | the version, 0 |
| 12-15 | how many pictures, N |
| 16-19 | the unit of the times below, in milliseconds; 0 means 1000 |
| 20-63 | nothing |
| 64 on | N + 1 entries of 8 bytes: a picture's time, in units, then where its JPEG starts in the file. The last has time `ffffffff` and says where the last JPEG ends |
| after | the JPEGs, back to back, in order |

Emby's own, and `ebb`'s, give a unit of 10000 ms and times 0, 1, 2 and on: a picture every 10 seconds.

Emby looks for one beside each video, named `<video>-<width>-<seconds>.bif`, and also finds a bare `<video>.bif`. Emby writes them there when a library's "save thumbnails with media" is on, which is what `SaveLocalThumbnailSets` is in its settings.

## Jellyfin's thumbnails

Jellyfin keeps them in its trickplay folder as sheets: grids of thumbnails, one JPEG a sheet.

```text
<trickplay>/<first two of the ID>/<item ID>/<width> - <columns>x<rows>/<n>.jpg
<trickplay>/ca/ca2ea61e-9a85-5010-2b76-538c60e3ca05/640 - 10x10/0.jpg
```

The ID and the folder names are in lower case. Thumbnail `k` is on sheet `k / (columns × rows)`, in column `k % columns` and row `(k % (columns × rows)) / columns`, and was taken `k` × the interval into the video. Every sheet is the full grid's size, the last blank after its last thumbnail.

Jellyfin's database, `jellyfin.db`, has a row for each set in `TrickplayInfos`: the item's ID in upper case, the thumbnails' width and height, the grid's columns and rows (`TileWidth`, `TileHeight`), how many there are (`ThumbnailCount`) and how far apart (`Interval`, in milliseconds). A video can have a set for each width. `BaseItems` has the item's path, as Jellyfin sees it.

A thumbnail is as wide as the setting asks, 640 or 320, unless the video is narrower: then it is the video's own width.

## Emby's database

`library.db` holds each item in `MediaItems`, with its path and a number for its kind. Emby does not write the numbers down; these were read off Emby 4.11's database:

| Number | Kind |
|---|---|
| 4 | a library |
| 5 | a film |
| 6 | a show, whose path is its folder |
| 8 | an episode |
| 10 | a trailer |
| 18 | another extra: a theme video, behind the scenes |

Each library's settings are JSON, in `ItemExtradata` under the type `LibraryOptions`, hung off the library's own item. `PathInfos` are its folders.

`ebb` reads the kinds and the paths from Emby's own indexes of them, which takes seconds; reading the items themselves would take minutes over a share. The settings are read from the libraries' own items, as they sit among every video's intro fingerprints.
