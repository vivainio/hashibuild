import os,shutil

def run(arg):
    cmd = 'go run hashibuild.go ' + arg
    print ">", cmd
    out = os.popen(cmd).read()
    print out
    return out

cfg = "--config test/testprj.json "
run(cfg)
manifest = run(cfg + "--manifest")

for part in ["buildsomething.cmd", "subdir/testfile.txt"]:
    assert part in manifest 

assert "ignored.txt" not in manifest

def nuke(pth):
    if os.path.isdir(pth):
        shutil.rmtree(pth)

outdir = "test/out"
nuke(outdir)

buildcmd = cfg + "--build"
run(buildcmd)
assert os.path.exists("test/out/testfile.txt")

archivedir = os.path.abspath("test/tmp")
os.environ["HASHIBUILD_ARCHIVE"] = archivedir

nuke(archivedir)
nuke(outdir)
withzipping = run(buildcmd)
assert "Zipping" in withzipping
run(buildcmd)
arccont = os.listdir(archivedir)

assert len(arccont) == 1 and "hashibuildtest" in arccont[0]
zipcont = os.popen("7za l %s/%s" % (archivedir, arccont[0])).read()
assert "testfile.txt" in zipcont
nuke(archivedir)


