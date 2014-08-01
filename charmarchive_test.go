// Copyright 2011, 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charm_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils/set"
	gc "launchpad.net/gocheck"
	"launchpad.net/goyaml"

	"gopkg.in/juju/charm.v3"
	charmtesting "gopkg.in/juju/charm.v3/testing"
)

type CharmArchiveSuite struct {
	archivePath string
}

var _ = gc.Suite(&CharmArchiveSuite{})

func (s *CharmArchiveSuite) SetUpSuite(c *gc.C) {
	s.archivePath = charmtesting.Charms.CharmArchivePath(c.MkDir(), "dummy")
}

var dummyManifest = []string{
	"actions.yaml",
	"config.yaml",
	"empty",
	"empty/.gitkeep",
	"hooks",
	"hooks/install",
	"metadata.yaml",
	"revision",
	"src",
	"src/hello.c",
}

func (s *CharmArchiveSuite) TestReadCharmArchive(c *gc.C) {
	archive, err := charm.ReadCharmArchive(s.archivePath)
	c.Assert(err, gc.IsNil)
	checkDummy(c, archive, s.archivePath)
}

func (s *CharmArchiveSuite) TestReadCharmArchiveWithoutConfig(c *gc.C) {
	// Technically varnish has no config AND no actions.
	// Perhaps we should make this more orthogonal?
	path := charmtesting.Charms.CharmArchivePath(c.MkDir(), "varnish")
	archive, err := charm.ReadCharmArchive(path)
	c.Assert(err, gc.IsNil)

	// A lacking config.yaml file still causes a proper
	// Config value to be returned.
	c.Assert(archive.Config().Options, gc.HasLen, 0)
}

func (s *CharmArchiveSuite) TestReadCharmArchiveWithoutActions(c *gc.C) {
	// Wordpress has config but no actions.
	path := charmtesting.Charms.CharmArchivePath(c.MkDir(), "wordpress")
	archive, err := charm.ReadCharmArchive(path)
	c.Assert(err, gc.IsNil)

	// A lacking actions.yaml file still causes a proper
	// Actions value to be returned.
	c.Assert(archive.Actions().ActionSpecs, gc.HasLen, 0)
}

func (s *CharmArchiveSuite) TestReadCharmArchiveBytes(c *gc.C) {
	data, err := ioutil.ReadFile(s.archivePath)
	c.Assert(err, gc.IsNil)

	archive, err := charm.ReadCharmArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	checkDummy(c, archive, "")
}

func (s *CharmArchiveSuite) TestManifest(c *gc.C) {
	archive, err := charm.ReadCharmArchive(s.archivePath)
	c.Assert(err, gc.IsNil)
	manifest, err := archive.Manifest()
	c.Assert(err, gc.IsNil)
	c.Assert(manifest, jc.DeepEquals, set.NewStrings(dummyManifest...))
}

func (s *CharmArchiveSuite) TestManifestNoRevision(c *gc.C) {
	archive, err := charm.ReadCharmArchive(s.archivePath)
	c.Assert(err, gc.IsNil)
	dirPath := c.MkDir()
	err = archive.ExpandTo(dirPath)
	c.Assert(err, gc.IsNil)
	err = os.Remove(filepath.Join(dirPath, "revision"))
	c.Assert(err, gc.IsNil)

	archive = extCharmArchiveDir(c, dirPath)
	manifest, err := archive.Manifest()
	c.Assert(err, gc.IsNil)
	c.Assert(manifest, gc.DeepEquals, set.NewStrings(dummyManifest...))
}

func (s *CharmArchiveSuite) TestManifestSymlink(c *gc.C) {
	srcPath := charmtesting.Charms.ClonedDirPath(c.MkDir(), "dummy")
	if err := os.Symlink("../target", filepath.Join(srcPath, "hooks/symlink")); err != nil {
		c.Skip("cannot symlink")
	}
	expected := append([]string{"hooks/symlink"}, dummyManifest...)

	archive := archiveDir(c, srcPath)
	manifest, err := archive.Manifest()
	c.Assert(err, gc.IsNil)
	c.Assert(manifest, gc.DeepEquals, set.NewStrings(expected...))
}

func (s *CharmArchiveSuite) TestExpandTo(c *gc.C) {
	archive, err := charm.ReadCharmArchive(s.archivePath)
	c.Assert(err, gc.IsNil)

	path := filepath.Join(c.MkDir(), "charm")
	err = archive.ExpandTo(path)
	c.Assert(err, gc.IsNil)

	dir, err := charm.ReadCharmDir(path)
	c.Assert(err, gc.IsNil)
	checkDummy(c, dir, path)
}

func (s *CharmArchiveSuite) prepareCharmArchive(c *gc.C, charmDir *charm.CharmDir, archivePath string) {
	file, err := os.Create(archivePath)
	c.Assert(err, gc.IsNil)
	defer file.Close()
	zipw := zip.NewWriter(file)
	defer zipw.Close()

	h := &zip.FileHeader{Name: "revision"}
	h.SetMode(syscall.S_IFREG | 0644)
	w, err := zipw.CreateHeader(h)
	c.Assert(err, gc.IsNil)
	_, err = w.Write([]byte(strconv.Itoa(charmDir.Revision())))

	h = &zip.FileHeader{Name: "metadata.yaml", Method: zip.Deflate}
	h.SetMode(0644)
	w, err = zipw.CreateHeader(h)
	c.Assert(err, gc.IsNil)
	data, err := goyaml.Marshal(charmDir.Meta())
	c.Assert(err, gc.IsNil)
	_, err = w.Write(data)
	c.Assert(err, gc.IsNil)

	for name := range charmDir.Meta().Hooks() {
		hookName := filepath.Join("hooks", name)
		h = &zip.FileHeader{
			Name:   hookName,
			Method: zip.Deflate,
		}
		// Force it non-executable
		h.SetMode(0644)
		w, err := zipw.CreateHeader(h)
		c.Assert(err, gc.IsNil)
		_, err = w.Write([]byte("not important"))
		c.Assert(err, gc.IsNil)
	}
}

func (s *CharmArchiveSuite) TestExpandToSetsHooksExecutable(c *gc.C) {
	charmDir := charmtesting.Charms.ClonedDir(c.MkDir(), "all-hooks")
	// CharmArchive manually, so we can check ExpandTo(), unaffected
	// by ArchiveTo()'s behavior
	archivePath := filepath.Join(c.MkDir(), "archive.charm")
	s.prepareCharmArchive(c, charmDir, archivePath)
	archive, err := charm.ReadCharmArchive(archivePath)
	c.Assert(err, gc.IsNil)

	path := filepath.Join(c.MkDir(), "charm")
	err = archive.ExpandTo(path)
	c.Assert(err, gc.IsNil)

	_, err = charm.ReadCharmDir(path)
	c.Assert(err, gc.IsNil)

	for name := range archive.Meta().Hooks() {
		hookName := string(name)
		info, err := os.Stat(filepath.Join(path, "hooks", hookName))
		c.Assert(err, gc.IsNil)
		perm := info.Mode() & 0777
		c.Assert(perm&0100 != 0, gc.Equals, true, gc.Commentf("hook %q is not executable", hookName))
	}
}

func (s *CharmArchiveSuite) TestCharmArchiveFileModes(c *gc.C) {
	// Apply subtler mode differences than can be expressed in Bazaar.
	srcPath := charmtesting.Charms.ClonedDirPath(c.MkDir(), "dummy")
	modes := []struct {
		path string
		mode os.FileMode
	}{
		{"hooks/install", 0751},
		{"empty", 0750},
		{"src/hello.c", 0614},
	}
	for _, m := range modes {
		err := os.Chmod(filepath.Join(srcPath, m.path), m.mode)
		c.Assert(err, gc.IsNil)
	}
	var haveSymlinks = true
	if err := os.Symlink("../target", filepath.Join(srcPath, "hooks/symlink")); err != nil {
		haveSymlinks = false
	}

	// CharmArchive and extract the charm to a new directory.
	archive := archiveDir(c, srcPath)
	path := c.MkDir()
	err := archive.ExpandTo(path)
	c.Assert(err, gc.IsNil)

	// Check sensible file modes once round-tripped.
	info, err := os.Stat(filepath.Join(path, "src", "hello.c"))
	c.Assert(err, gc.IsNil)
	c.Assert(info.Mode()&0777, gc.Equals, os.FileMode(0644))
	c.Assert(info.Mode()&os.ModeType, gc.Equals, os.FileMode(0))

	info, err = os.Stat(filepath.Join(path, "hooks", "install"))
	c.Assert(err, gc.IsNil)
	c.Assert(info.Mode()&0777, gc.Equals, os.FileMode(0755))
	c.Assert(info.Mode()&os.ModeType, gc.Equals, os.FileMode(0))

	info, err = os.Stat(filepath.Join(path, "empty"))
	c.Assert(err, gc.IsNil)
	c.Assert(info.Mode()&0777, gc.Equals, os.FileMode(0755))

	if haveSymlinks {
		target, err := os.Readlink(filepath.Join(path, "hooks", "symlink"))
		c.Assert(err, gc.IsNil)
		c.Assert(target, gc.Equals, "../target")
	}
}

func (s *CharmArchiveSuite) TestCharmArchiveRevisionFile(c *gc.C) {
	charmDir := charmtesting.Charms.ClonedDirPath(c.MkDir(), "dummy")
	revPath := filepath.Join(charmDir, "revision")

	// Missing revision file
	err := os.Remove(revPath)
	c.Assert(err, gc.IsNil)

	archive := extCharmArchiveDir(c, charmDir)
	c.Assert(archive.Revision(), gc.Equals, 0)

	// Missing revision file with old revision in metadata
	file, err := os.OpenFile(filepath.Join(charmDir, "metadata.yaml"), os.O_WRONLY|os.O_APPEND, 0)
	c.Assert(err, gc.IsNil)
	_, err = file.Write([]byte("\nrevision: 1234\n"))
	c.Assert(err, gc.IsNil)

	archive = extCharmArchiveDir(c, charmDir)
	c.Assert(archive.Revision(), gc.Equals, 1234)

	// Revision file with bad content
	err = ioutil.WriteFile(revPath, []byte("garbage"), 0666)
	c.Assert(err, gc.IsNil)

	path := extCharmArchiveDirPath(c, charmDir)
	archive, err = charm.ReadCharmArchive(path)
	c.Assert(err, gc.ErrorMatches, "invalid revision file")
	c.Assert(archive, gc.IsNil)
}

func (s *CharmArchiveSuite) TestCharmArchiveSetRevision(c *gc.C) {
	archive, err := charm.ReadCharmArchive(s.archivePath)
	c.Assert(err, gc.IsNil)

	c.Assert(archive.Revision(), gc.Equals, 1)
	archive.SetRevision(42)
	c.Assert(archive.Revision(), gc.Equals, 42)

	path := filepath.Join(c.MkDir(), "charm")
	err = archive.ExpandTo(path)
	c.Assert(err, gc.IsNil)

	dir, err := charm.ReadCharmDir(path)
	c.Assert(err, gc.IsNil)
	c.Assert(dir.Revision(), gc.Equals, 42)
}

func (s *CharmArchiveSuite) TestExpandToWithBadLink(c *gc.C) {
	charmDir := charmtesting.Charms.ClonedDirPath(c.MkDir(), "dummy")
	badLink := filepath.Join(charmDir, "hooks", "badlink")

	// Symlink targeting a path outside of the charm.
	err := os.Symlink("../../target", badLink)
	c.Assert(err, gc.IsNil)

	archive := extCharmArchiveDir(c, charmDir)
	c.Assert(err, gc.IsNil)

	path := filepath.Join(c.MkDir(), "charm")
	err = archive.ExpandTo(path)
	c.Assert(err, gc.ErrorMatches, `cannot extract "hooks/badlink": symlink "../../target" leads out of scope`)

	// Symlink targeting an absolute path.
	os.Remove(badLink)
	err = os.Symlink("/target", badLink)
	c.Assert(err, gc.IsNil)

	archive = extCharmArchiveDir(c, charmDir)
	c.Assert(err, gc.IsNil)

	path = filepath.Join(c.MkDir(), "charm")
	err = archive.ExpandTo(path)
	c.Assert(err, gc.ErrorMatches, `cannot extract "hooks/badlink": symlink "/target" is absolute`)
}

func extCharmArchiveDirPath(c *gc.C, dirpath string) string {
	path := filepath.Join(c.MkDir(), "archive.charm")
	cmd := exec.Command("/bin/sh", "-c", fmt.Sprintf("cd %s; zip --fifo --symlinks -r %s .", dirpath, path))
	output, err := cmd.CombinedOutput()
	c.Assert(err, gc.IsNil, gc.Commentf("Command output: %s", output))
	return path
}

func extCharmArchiveDir(c *gc.C, dirpath string) *charm.CharmArchive {
	path := extCharmArchiveDirPath(c, dirpath)
	archive, err := charm.ReadCharmArchive(path)
	c.Assert(err, gc.IsNil)
	return archive
}

func archiveDir(c *gc.C, dirpath string) *charm.CharmArchive {
	dir, err := charm.ReadCharmDir(dirpath)
	c.Assert(err, gc.IsNil)
	buf := new(bytes.Buffer)
	err = dir.ArchiveTo(buf)
	c.Assert(err, gc.IsNil)
	archive, err := charm.ReadCharmArchiveBytes(buf.Bytes())
	c.Assert(err, gc.IsNil)
	return archive
}