### 10.32 Image (`std.image`)

`std.image` detects common image formats and parses header metadata. Full
pixel decoding is intentionally left for a future runtime-backed codec layer.

Supported metadata formats:

- PNG
- JPEG
- GIF
- BMP
- WebP

Core API:

```osty
image.identify(data) -> Format
image.formatName(format) -> String
image.parse(data) -> Result<Metadata, Error>
image.dimensions(data) -> Result<Size, Error>
image.isImage(data) -> Bool
image.isPng(data) -> Bool
image.isJpeg(data) -> Bool
image.isGif(data) -> Bool
image.isBmp(data) -> Bool
image.isWebp(data) -> Bool
```

Behavior:

- `parse` returns width, height, approximate bits-per-pixel, alpha flag, and
  animation flag where the container exposes it cheaply.
- PNG APNG and GIF multi-image files are marked animated by header scanning.
- Unknown or truncated images return `Error`.
