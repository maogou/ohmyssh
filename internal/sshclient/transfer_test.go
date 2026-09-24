package sshclient

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests run against the in-process SFTP server the harness in
// client_test.go stands up, so the transfer engine is exercised over the real
// protocol rather than against a mock. The root the server serves is a
// t.TempDir the test keeps in hand, which is what lets it assert on the files
// that landed.
//
// Remote paths are relative throughout: pkg/sftp joins the server's working
// directory onto a relative path and lets an absolute one through to the real
// filesystem.

// transferFixture connects an SFTP client to a server serving a fresh temporary
// directory, and returns the client and that directory.
func transferFixture(t *testing.T) (*SFTPClient, string) {
	t.Helper()

	root := t.TempDir()
	pub, keyPath := newTestClientKey(t)
	server, _ := newTestServerWithFS(t, pub, root)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	session, err := NewSFTPClient(client)
	if err != nil {
		t.Fatalf("NewSFTPClient: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, root
}

// writeTree writes files, keyed by path relative to dir, creating the
// directories they need.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// readTree reads every regular file under dir, keyed by slash-separated path
// relative to it, so a whole tree can be compared in one assertion.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()

	files := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return files
}

// collect records every progress report so a test can assert on the sequence.
func collect() (ProgressFunc, *[]Progress) {
	var reports []Progress
	return func(p Progress) { reports = append(reports, p) }, &reports
}

// A whole tree goes up and comes back down unchanged, directories included:
// this is the round trip the feature exists for.
func TestTransferRoundTrip(t *testing.T) {
	session, root := transferFixture(t)

	source := t.TempDir()
	want := map[string]string{
		"README.md":              "# hello\n",
		"internal/app.go":        "package internal\n",
		"internal/deep/deep.txt": strings.Repeat("deep ", 500),
		"internal/empty.txt":     "",
	}
	writeTree(t, source, want)

	// Up into a directory that does not exist yet: the plan creates it.
	plan, err := session.PlanUpload(source, "uploaded")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if plan.Files() != len(want) {
		t.Errorf("plan has %d files, want %d", plan.Files(), len(want))
	}
	if err := session.Run(context.Background(), plan, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if got := readTree(t, filepath.Join(root, "uploaded")); !equalTrees(got, want) {
		t.Errorf("uploaded tree = %v, want %v", got, want)
	}

	// And back down again.
	dest := t.TempDir()
	down, err := session.PlanDownload("uploaded", dest)
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	if err := session.Run(context.Background(), down, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if got := readTree(t, dest); !equalTrees(got, want) {
		t.Errorf("downloaded tree = %v, want %v", got, want)
	}
}

func equalTrees(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for name, content := range want {
		if got[name] != content {
			return false
		}
	}
	return true
}

// The trailing slash is what decides where a single file lands, the way it does
// with scp. A directory copies its contents into the remote path either way.
func TestPlanUploadPathRules(t *testing.T) {
	session, _ := transferFixture(t)

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "a", "sub/b.txt": "b"})
	file := filepath.Join(dir, "a.txt")

	tests := []struct {
		name      string
		local     string
		remote    string
		wantItems int
		wantDest  string
		wantDirs  []string
		wantBytes int64
	}{
		{
			name: "file keeps its own name inside a directory that ends in a slash",
			// The remote end is the slash-terminated one, so the base name joins it.
			local: file, remote: "dest/", wantItems: 1, wantDest: "dest/a.txt", wantBytes: 1,
		},
		{
			// Nothing is there to stat, so the path is the file being created.
			name:  "file without a trailing slash names a destination that is not there",
			local: file, remote: "dest/renamed.txt", wantItems: 1, wantDest: "dest/renamed.txt", wantBytes: 1,
		},
		{
			name:  "an empty remote path is the working directory",
			local: file, remote: "", wantItems: 1, wantDest: "a.txt", wantBytes: 1,
		},
		{
			name:   "a directory copies its contents, not the directory",
			local:  dir,
			remote: "dest",
			// a.txt and sub/b.txt; the remote dirs are dest and dest/sub, parents
			// first so the transfer can create them in order.
			wantItems: 2, wantDirs: []string{"dest", "dest/sub"}, wantBytes: 2,
		},
		{
			name:   "a trailing slash on the remote end changes nothing for a directory",
			local:  dir,
			remote: "dest/",
			// The slash is trimmed before the contents are laid out, so this is
			// the same transfer as the one above rather than dest/dest.
			wantItems: 2, wantDirs: []string{"dest", "dest/sub"}, wantBytes: 2,
		},
		{
			// Trimming the slash cannot be allowed to empty the path: "/" names the
			// filesystem root, and reading it as the empty path would put the tree
			// in the login directory, where it lands looking like a success. Nothing
			// is written here — the plan is what says where the transfer would go,
			// which is as much as a test can ask of the root.
			name:      "the filesystem root is a destination, not the working directory",
			local:     dir,
			remote:    "/",
			wantItems: 2, wantDest: "/a.txt",
			wantDirs: []string{"/", "/sub"}, wantBytes: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := session.PlanUpload(tt.local, tt.remote)
			if err != nil {
				t.Fatalf("PlanUpload: %v", err)
			}
			if plan.Files() != tt.wantItems {
				t.Errorf("files = %d, want %d", plan.Files(), tt.wantItems)
			}
			if plan.Bytes != tt.wantBytes {
				t.Errorf("bytes = %d, want %d", plan.Bytes, tt.wantBytes)
			}
			if tt.wantDest != "" && plan.Items[0].Dest != tt.wantDest {
				t.Errorf("dest = %q, want %q", plan.Items[0].Dest, tt.wantDest)
			}
			if tt.wantDirs != nil && !equalStrings(plan.Dirs, tt.wantDirs) {
				t.Errorf("dirs = %v, want %v", plan.Dirs, tt.wantDirs)
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A local path that is not there is reported by the plan, before a connection is
// opened or a byte is written.
func TestPlanUploadReportsAMissingSource(t *testing.T) {
	session, _ := transferFixture(t)

	_, err := session.PlanUpload(filepath.Join(t.TempDir(), "absent"), "dest")
	if err == nil {
		t.Fatal("PlanUpload accepted a path that does not exist")
	}
	if !strings.Contains(err.Error(), "local path") {
		t.Errorf("error = %v, want it to name the local path", err)
	}
}

func TestPlanDownloadPathRules(t *testing.T) {
	session, root := transferFixture(t)
	writeTree(t, root, map[string]string{"logs/app.log": "log line\n", "logs/old/app.log": "old\n"})

	// A single file into a path that ends in a separator keeps its own name.
	dest := t.TempDir()
	plan, err := session.PlanDownload("logs/app.log", dest+string(os.PathSeparator))
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	if want := filepath.Join(dest, "app.log"); plan.Items[0].Dest != want {
		t.Errorf("dest = %q, want %q", plan.Items[0].Dest, want)
	}

	// A directory copies its contents into the local path, trimming the
	// separator so the tree is not nested inside a directory of its own name.
	into := t.TempDir()
	dirPlan, err := session.PlanDownload("logs", into+string(os.PathSeparator))
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	if dirPlan.Files() != 2 {
		t.Errorf("files = %d, want 2", dirPlan.Files())
	}
	for _, item := range dirPlan.Items {
		if !strings.HasPrefix(item.Dest, into+string(os.PathSeparator)) {
			t.Errorf("dest %q is not under %q", item.Dest, into)
		}
		if strings.HasPrefix(item.Dest, filepath.Join(into, "logs")) {
			t.Errorf("dest %q repeats the source directory name", item.Dest)
		}
	}

	// A remote path that is not there fails while measuring, not while copying.
	if _, err := session.PlanDownload("absent", t.TempDir()); err == nil {
		t.Fatal("PlanDownload accepted a path that does not exist")
	}

	// The filesystem root is a local destination like any other, and stays itself
	// rather than being read as the empty path, which would unpack the tree into
	// the current directory. Only the plan is asked: running it would write to the
	// real root, which is not something a test may do.
	atRoot, err := session.PlanDownload("logs", string(os.PathSeparator))
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	rootDirs := []string{string(os.PathSeparator), filepath.Join(string(os.PathSeparator), "old")}
	if !equalStrings(atRoot.Dirs, rootDirs) {
		t.Errorf("dirs = %v, want %v", atRoot.Dirs, rootDirs)
	}
	for _, item := range atRoot.Items {
		if !strings.HasPrefix(item.Dest, string(os.PathSeparator)) {
			t.Errorf("dest %q is not under the filesystem root", item.Dest)
		}
	}
}

// The totals go out before the first byte so a bar has its denominator, and the
// last report closes the file and the transfer together.
func TestTransferProgressReportsTotalsFirstAndLast(t *testing.T) {
	session, _ := transferFixture(t)

	source := t.TempDir()
	writeTree(t, source, map[string]string{
		"one.bin": strings.Repeat("x", copyBufferSize*3),
		"two.bin": strings.Repeat("y", 10),
	})

	plan, err := session.PlanUpload(source, "dest")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	report, reports := collect()

	// The throttle is on a wall clock, so the test drives the reporter directly:
	// what is under test here is the sequence, not the timing.
	if err := session.Run(context.Background(), plan, func(p Progress) { report(p) }); err != nil {
		t.Fatalf("upload: %v", err)
	}

	if len(*reports) == 0 {
		t.Fatal("no progress was reported")
	}
	first := (*reports)[0]
	if first.TotalBytes != plan.Bytes || first.FilesTotal != 2 {
		t.Errorf("first report = %d/%d bytes over %d files, want %d over 2",
			first.TotalDone, first.TotalBytes, first.FilesTotal, plan.Bytes)
	}
	if first.TotalDone != 0 || first.FilesDone != 0 {
		t.Errorf("first report claims %d bytes and %d files already done", first.TotalDone, first.FilesDone)
	}

	last := (*reports)[len(*reports)-1]
	if !last.Done {
		t.Error("the last report does not close a file")
	}
	if last.FileDone != last.FileTotal {
		t.Errorf("last report leaves the file at %d of %d", last.FileDone, last.FileTotal)
	}
	if last.TotalDone != plan.Bytes {
		t.Errorf("last report moved %d bytes, want %d", last.TotalDone, plan.Bytes)
	}
	if last.FilesDone != 2 {
		t.Errorf("last report counted %d files, want 2", last.FilesDone)
	}
	if last.Rate <= 0 {
		t.Errorf("last report has no rate to show")
	}
}

// Cancelling stops the transfer at a chunk boundary and says so, rather than
// leaving the caller to work out that the missing bytes were not an error.
func TestTransferStopsWhenCancelled(t *testing.T) {
	session, _ := transferFixture(t)

	source := t.TempDir()
	writeTree(t, source, map[string]string{
		"big.bin":  strings.Repeat("z", copyBufferSize*20),
		"last.bin": "never sent\n",
	})
	plan, err := session.PlanUpload(source, "dest")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, reports := collect()
	err = session.Run(ctx, plan, func(Progress) {
		// Cancelling from inside a report is the shape the browser uses: the
		// user presses esc while the copy loop is running.
		if len(*reports) == 0 {
			*reports = append(*reports, Progress{})
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}

// An interrupted transfer leaves nothing half-written behind, in either
// direction, and each direction needs its own answer to how.
//
// The interrupt is a cancelled context, and it is delivered the moment the
// transfer is demonstrably under way — see waitForContent for why the tests wait
// on the file rather than on a clock.
func TestInterruptedUploadIsRemovedFromTheHost(t *testing.T) {
	session, root := transferFixture(t)

	// The remote file goes in under its real name — renaming on the far side is
	// not something every server does the same way — so a copy that does not
	// finish has to take it away again. A file under the right name holding part
	// of the contents reads as a complete one to whoever finds it next, which is
	// worse than no file at all.
	source := t.TempDir()
	local := filepath.Join(source, "big.bin")
	sparseFile(t, local, copyBufferSize*4000)

	if err := os.MkdirAll(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatalf("mkdir the destination: %v", err)
	}
	plan, err := session.PlanUpload(local, "dest/big.bin")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}

	remote := filepath.Join(root, "dest", "big.bin")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	arrived := make(chan bool, 1)
	go func() {
		arrived <- waitForContent(remote, 2*time.Second)
		cancel()
	}()

	err = session.Run(ctx, plan, nil)
	if !<-arrived {
		t.Fatal("the upload never wrote a byte to the host, so nothing was interrupted")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	if _, err := os.Stat(remote); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the interrupted upload is still on the host: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "dest"))
	if err != nil {
		t.Fatalf("read the destination directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the destination directory holds %v, want nothing", entries)
	}
}

// The download side, where the file that would be left behind is a local one and
// the damage is different: a half-written download under the destination's own
// name is exactly what a completed one looks like, so it is written beside the
// destination and only given the name once it is whole. An interrupted download
// must therefore leave the file that was already there untouched.
func TestInterruptedDownloadLeavesTheDestinationAlone(t *testing.T) {
	session, root := transferFixture(t)

	// Long enough to still be arriving when the cancel lands. Sparse, so it costs
	// nothing to make and only the first packets are ever read.
	sparseFile(t, filepath.Join(root, "big.bin"), copyBufferSize*4000)

	dest := t.TempDir()
	target := filepath.Join(dest, "big.bin")
	const original = "what was here before\n"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatalf("write the file being replaced: %v", err)
	}

	plan, err := session.PlanDownload("big.bin", target)
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}

	partial := target + partialSuffix
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	arrived := make(chan bool, 1)
	go func() {
		arrived <- waitForContent(partial, 2*time.Second)
		cancel()
	}()

	err = session.Run(ctx, plan, nil)
	if !<-arrived {
		t.Fatal("the download never wrote a byte, so nothing was interrupted")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}

	if _, err := os.Stat(partial); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a half-finished download was left at %s: %v", partial, err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read the destination: %v", err)
	}
	if string(content) != original {
		t.Errorf("the destination holds %q, want it left as it was", content)
	}
}

// waitForContent reports whether path turned up holding bytes within timeout.
//
// The wait is on the file rather than on a clock, so the interrupt lands while
// the copy is running: a fixed sleep would either be longer than the transfer,
// leaving nothing to interrupt, or a guess at how long the first packet takes.
func waitForContent(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

// sparseFile creates a file of size bytes that occupies almost no disk. The
// interrupted transfers need a source long enough to still be moving when they
// are cut off, and nothing past the first packets is ever read.
func sparseFile(t *testing.T, path string, size int64) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Truncate(size); err != nil {
		t.Fatalf("size %s to %d: %v", path, size, err)
	}
}

// A server with no sftp subsystem is what the exec fallback exists for. When the
// fallback is refused too the failure has to say what was attempted, because
// that message is all the user has to go on.
func TestSFTPReportsWhenNoSubsystemIsAvailable(t *testing.T) {
	pub, keyPath := newTestClientKey(t)
	// An empty root refuses the subsystem request, so every fallback path is
	// tried and rejected by the fake shell, which knows no such command.
	server, _ := newTestServerWithFS(t, pub, "")
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")

	client, err := dialTestHost(t, server.Addr(), keyPath, knownHosts)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	session, err := NewSFTPClient(client)
	if err == nil {
		_ = session.Close()
		t.Fatal("NewSFTPClient succeeded against a host with no sftp")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sftp") {
		t.Errorf("error = %v, want it to mention sftp", err)
	}
}

// throttle is what keeps a large transfer from spending more time rendering than
// moving bytes. It must not, however, drop the report that closes a file: the
// bar would be left a chunk short of the end, which reads as a stall.
func TestThrottleDropsIntermediateReportsButKeepsTheClosingOne(t *testing.T) {
	report, reports := collect()
	throttled := throttle(report, time.Hour)

	for i := range 5 {
		throttled(Progress{FileDone: int64(i)})
	}
	throttled(Progress{FileDone: 5, Done: true})
	throttled(Progress{FileDone: 6})

	// The first sample goes through (there is no previous one to measure
	// against), then the interval swallows the rest, then the closing report goes
	// through, then the interval swallows what follows it.
	if len(*reports) != 2 {
		t.Fatalf("reports = %d, want 2: %+v", len(*reports), *reports)
	}
	if (*reports)[0].FileDone != 0 {
		t.Errorf("first report is %d, want the first sample offered", (*reports)[0].FileDone)
	}
	if !(*reports)[1].Done {
		t.Errorf("second report = %+v, want the one that closes the file", (*reports)[1])
	}
}

func TestThrottlePassesNothingWithoutAReporter(t *testing.T) {
	if throttle(nil, time.Second) != nil {
		t.Error("throttle built a reporter around nothing")
	}
}

// A nil reporter has to be allowed: measuring a plan and copying it are useful
// on their own, and the browser passes no reporter until it has a frame to draw
// into.
func TestRunAcceptsNoReporter(t *testing.T) {
	session, root := transferFixture(t)

	source := t.TempDir()
	writeTree(t, source, map[string]string{"a.txt": "a"})
	plan, err := session.PlanUpload(source, "dest")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if err := session.Run(context.Background(), plan, nil); err != nil {
		t.Fatalf("Run with no reporter: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "dest", "a.txt")); err != nil {
		t.Errorf("the file did not arrive: %v", err)
	}
}

// A transfer moves a file that is already on the other end: the destination is
// truncated and rewritten, the way scp leaves it.
func TestTransferOverwritesAnExistingFile(t *testing.T) {
	session, root := transferFixture(t)
	writeTree(t, root, map[string]string{"dest/app.txt": strings.Repeat("old", 100)})

	source := t.TempDir()
	writeTree(t, source, map[string]string{"app.txt": "new\n"})

	plan, err := session.PlanUpload(filepath.Join(source, "app.txt"), "dest/app.txt")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if err := session.Run(context.Background(), plan, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "dest", "app.txt"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got := string(content); got != "new\n" {
		t.Errorf("file = %q, want the new content with no tail of the old", got)
	}
}

// A symlink to a directory is not followed: a link pointing back up its own tree
// would otherwise recurse until the transfer ran out of file descriptors.
func TestPlanUploadDoesNotFollowDirectorySymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}

	session, _ := transferFixture(t)

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"real/a.txt": "a"})
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	// A link that resolves to nothing is passed over rather than failing the
	// whole transfer.
	if err := os.Symlink(filepath.Join(dir, "absent"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	plan, err := session.PlanUpload(dir, "dest")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	// Only the real file is planned: the link to the directory is not followed
	// and the dangling one is passed over. The destinations are what tell them
	// apart, since the temporary directory's own name contains "link".
	if plan.Files() != 1 {
		t.Fatalf("files = %d, want 1: %+v", plan.Files(), plan.Items)
	}
	if got := plan.Items[0].Dest; got != "dest/real/a.txt" {
		t.Errorf("dest = %q, want dest/real/a.txt", got)
	}
}

// A symlink to a regular file is a file: it is copied by value, so the
// destination is a real file rather than a link pointing at wherever the source
// pointed.
func TestPlanUploadCopiesFileSymlinksByValue(t *testing.T) {
	session, _ := transferFixture(t)

	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"real.txt": "content\n"})
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}

	plan, err := session.PlanUpload(dir, "dest")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if plan.Files() != 2 {
		t.Fatalf("files = %d, want the file and the link to it", plan.Files())
	}
	if plan.Bytes != 16 {
		t.Errorf("bytes = %d, want the content counted twice", plan.Bytes)
	}
}

// PlanDownload needs the connection, so the failure it reports for a broken
// remote path is the server's. It must still be clear which end was at fault.
func TestPlanDownloadNamesTheRemoteEnd(t *testing.T) {
	session, _ := transferFixture(t)

	_, err := session.PlanDownload("nowhere", t.TempDir())
	if err == nil {
		t.Fatal("PlanDownload accepted a path that does not exist")
	}
	if !strings.Contains(err.Error(), "remote path") {
		t.Errorf("error = %v, want it to name the remote path", err)
	}
}

// Sized to one buffer plus a little, so the copy loop runs more than once and
// the file is not handed over in a single chunk.
func TestTransferMovesMoreThanOneBuffer(t *testing.T) {
	session, root := transferFixture(t)

	content := bytes.Repeat([]byte("0123456789"), copyBufferSize/5)
	source := t.TempDir()
	writeTree(t, source, map[string]string{"big.bin": string(content)})

	plan, err := session.PlanUpload(source, "dest")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if err := session.Run(context.Background(), plan, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(root, "dest", "big.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("file is %d bytes, want %d, and differs in content", len(got), len(content))
	}
}

// A plan built for nothing moves nothing and reports nothing, which is what
// makes an empty directory a successful transfer rather than a failure.
func TestRunWithAnEmptyPlan(t *testing.T) {
	session, _ := transferFixture(t)

	source := t.TempDir()
	plan, err := session.PlanUpload(source, "dest")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if plan.Files() != 0 {
		t.Fatalf("files = %d, want 0", plan.Files())
	}
	if err := session.Run(context.Background(), plan, nil); err != nil {
		t.Errorf("Run of an empty plan: %v", err)
	}
	if err := session.Run(context.Background(), nil, nil); err != nil {
		t.Errorf("Run of no plan: %v", err)
	}
}

// The fixture has to be able to produce an SFTP session at all, or every test
// above would be passing for the wrong reason.
func TestTransferFixtureIsUsable(t *testing.T) {
	session, root := transferFixture(t)

	if root == "" {
		t.Fatal("the fixture served no directory")
	}
	names, err := session.ReadDir(".")
	if err != nil {
		t.Fatalf("read the served directory: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("a fresh root holds %d entries, want none: %v", len(names), names)
	}
}

// A file going into a directory that is already there keeps its own name,
// whether or not the path ends in a slash. Reading the slash alone is what made
// "put README.md /root" fail with an SFTP error that gave no hint the problem
// was the destination being a directory — cp, scp and rsync all stat the
// destination and copy inside it.
func TestPlanUploadIntoAnExistingDirectory(t *testing.T) {
	session, root := transferFixture(t)
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	source := t.TempDir()
	writeTree(t, source, map[string]string{"README.md": "hello\n"})

	// The name arrives under the directory with no trailing slash to say so.
	plan, err := session.PlanUpload(filepath.Join(source, "README.md"), "live")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if want := "live/README.md"; plan.Items[0].Dest != want {
		t.Errorf("dest = %q, want %q", plan.Items[0].Dest, want)
	}

	if err := session.Run(context.Background(), plan, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "live", "README.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(content) != "hello\n" {
		t.Errorf("content = %q, want %q", content, "hello\n")
	}
	// The directory is still a directory: it was added to, not written over.
	if info, err := os.Stat(filepath.Join(root, "live")); err != nil || !info.IsDir() {
		t.Errorf("the destination is no longer a directory: %v", err)
	}
}

// A path that is not there is still the name of the file being created, which is
// the case the directory rule must not swallow.
func TestPlanUploadCreatesAMissingPath(t *testing.T) {
	session, _ := transferFixture(t)

	source := t.TempDir()
	writeTree(t, source, map[string]string{"a.txt": "a"})

	plan, err := session.PlanUpload(filepath.Join(source, "a.txt"), "not-there")
	if err != nil {
		t.Fatalf("PlanUpload: %v", err)
	}
	if want := "not-there"; plan.Items[0].Dest != want {
		t.Errorf("dest = %q, want %q", plan.Items[0].Dest, want)
	}
}

// The download side has the same rule at the other end: "get host:/etc/hosts
// /tmp" means /tmp/hosts, not opening /tmp for writing.
func TestPlanDownloadIntoAnExistingDirectory(t *testing.T) {
	session, root := transferFixture(t)
	writeTree(t, root, map[string]string{"etc/hosts": "127.0.0.1 localhost\n"})

	into := t.TempDir()
	plan, err := session.PlanDownload("etc/hosts", into)
	if err != nil {
		t.Fatalf("PlanDownload: %v", err)
	}
	want := filepath.Join(into, "hosts")
	if plan.Items[0].Dest != want {
		t.Errorf("dest = %q, want %q", plan.Items[0].Dest, want)
	}

	if err := session.Run(context.Background(), plan, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("the file did not arrive under the directory: %v", err)
	}
}
