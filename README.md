# hashibuild
Hash initiated build. Avoid recompiling artifacts when source tree hash hasn't changed.

The basic idea is that your build artifacts in "outputDir" are a direct function of a set of your source files. We perform a deep tree checksum of your source tree and zip up the output directory with the name derived from the checksum.

So we:

1. Count the hash for the input files (sources, yarn.lock etc)
2. If a zip file corresponding with the hash is found in the "archive" directory, we just unzip that zip file to output directory.
3. If a zip file is not found, we run the build command ('buildCmd' value) in the 'inputRoot' directory and create the zip file from the new artifacts
4. On next run, the build will be skipped since we can use the zip file generated by phase #3.


## Installation

```
$ zippim get https://github.com/vivainio/hashibuild/releases/download/v0.1/hashibuild.exe
```

Or just drop the exe file to PATH.

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
  -salt string
        Provide salt string to invalidate hashes that would otherwise be same
  -treehash string
        Show manifest for specified path (no config needed)
  -vacuum
        Clean up archive directory from old/big files
```

Example configuration file:

```
{
    "name": "myapp",
    "inputRoot": "../../myapp",
    "include": ["src", "yarn.lock"],
    "outputDir": "../../myapp/published",
    "buildCmd": ".\\Build.cmd buildapp",
    "exclude": ["src/Tests", "src/templates.ts"]
}
```

## Archives

There are 2 environment variables that specify the archive directory.

`HASHIBUILD_ARCHIVE` is the local archive directory. This is where all the created zip files are
saved.

`HASHIBUILD_ARCHIVE_REMOTE` is the remote archive url pattern. If the wanted file doesn't exist
in local archive directory, we perform HTTP get to copy thi file to local archive and then
use that.

The pattern looks like `https://github.com/vivainio/hashibuild/raw/master/test/fakeremote/[ZIP]`, where `[ZIP]`
will be replaced by the archive name.

## Uploader

If you set environment variable `HASHIBUILD_UPLOADER` with value like `mv [ZIP] /tmp/to_upload`, after a succesfull
build this command will be executed with [ZIP] replaced by absolute path of the generated zip file.

You can use this to trigger custom command that either uploads the file directly to `HASHIBUILD_ARCHIVE_REMOTE`
(slowing the build), or moves it to a place that you can batch upload the files from at your leisure.

## Ignored files

This only works in git directories; we use "git ls-files" in the inputRoot directory to leverage .gitignore rules.

You can use 'include' and 'exclude' rules to finetune the non-gitignored file set. Simple case-insensitive prefix
match is used. Include rules are the first filter, after which exclude rules are applied.