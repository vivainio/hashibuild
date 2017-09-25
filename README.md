# hashibuild
Hash initiated build. Avoid recompiling artifacts when source tree hash hasn't changed

## Installation

```
$ zippim get https://github.com/vivainio/hashibuild/releases/download/v0.1/hashibuild.exe
```

Or just drop the file to PATH.

## Usage

```
λ hashibuild -h
Usage of C:\PKG\bin\hashibuild.exe:
  -archive string
        Archive root dir (needed if HASHIBUILD_ARCHIVE env var is not set)
  -build
        Run build
  -config string
        Json config file
  -manifest
        Show manifest (requires --config)
  -treehash string
        Show manifest for specified path (no config needed)
```

Example configuration file:

```
{
    "name": "myapp",
    "inputRoot": "../../myapp",
    "inputPaths": ["src", "yarn.lock"],
    "outputDir": "../../myapp/published",
    "buildCmd": ".\\Build.cmd",
    "ignores": ["src/Tests", "src/templates.ts"]
}
```

