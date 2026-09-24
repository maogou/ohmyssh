package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	// copyBufferSize is the unit a transfer moves in. It is pkg/sftp's packet
	// size — its default MaxPacket, and what the far end sees as one request —
	// and the size the tests measure a file in when they need more than one.
	copyBufferSize = 32 * 1024

	// partialSuffix is what a download is called while it is still arriving.
	partialSuffix = ".part"

	// progressInterval is how often a running transfer is allowed to report.
	// Without it a gigabyte would produce thirty thousand reports and spend
	// more time rendering than moving bytes.
	progressInterval = 100 * time.Millisecond
)

// Direction says which way a transfer moves bytes.
type Direction int

const (
	// Upload sends a local path to a host.
	Upload Direction = iota
	// Download brings a path from a host to the local machine.
	Download
)

// Verb names the direction the way the command line spells it, so a summary
// reads the same whichever side the bytes came from.
func (d Direction) Verb() string {
	if d == Download {
		return "get"
	}
	return "put"
}

// TransferItem is one file in a transfer plan. Source is on the sending side and
// Dest on the receiving one, whichever those turn out to be.
type TransferItem struct {
	Source string
	Dest   string
	Size   int64
}

// Plan is a transfer measured before any byte moves.
//
// Measuring first is what gives the overall progress bar a denominator. It also
// means a path that cannot be read is reported while nothing has been written
// yet, rather than half way through.
type Plan struct {
	Direction Direction
	Items     []TransferItem
	// Dirs are the directories the transfer has to create before it can write,
	// parents first. They are remote paths for an upload and local ones for a
	// download: the direction decides which end they belong to.
	Dirs []string
	// Bytes is the total the items add up to.
	Bytes int64
}

// Files is how many files the plan moves.
func (p *Plan) Files() int {
	if p == nil {
		return 0
	}
	return len(p.Items)
}

// Progress is one report of how a transfer is going.
//
// It carries the file and the whole transfer at once. The rate and the ETA are
// worked out here rather than by each caller, so the command line and the
// browser show the same numbers from the same arithmetic.
type Progress struct {
	File      string
	FileDone  int64
	FileTotal int64
	// Done marks the report that closes a file, so a renderer can be sure the
	// file's bar reaches the end and a throttled reporter knows to let it past.
	Done bool

	TotalDone  int64
	TotalBytes int64
	FilesDone  int
	FilesTotal int

	Rate float64       // bytes per second
	ETA  time.Duration // zero until the rate means something
}

// ProgressFunc receives progress reports. The copy loop calls it between
// chunks, so an implementation must not block for long.
type ProgressFunc func(Progress)

// throttle limits a progress reporter to a steady rate. The report that closes a
// file always gets through: dropping it would leave a file's bar one chunk short
// of the end, which reads as a stalled transfer.
func throttle(fn ProgressFunc, every time.Duration) ProgressFunc {
	if fn == nil {
		return nil
	}
	var last time.Time
	return func(p Progress) {
		now := time.Now()
		if !p.Done && now.Sub(last) < every {
			return
		}
		last = now
		fn(p)
	}
}

// PlanUpload measures a local path against where it is going.
//
// What lands where follows cp, scp and rsync. A directory's contents copy into
// the remote path, while a file keeps its own name when the remote path is a
// directory — whether that is because the path ends in a slash or because the
// directory is already there.
//
// It takes a connection because of that last case: whether a path names a
// directory is not something the string can say. Reading the trailing slash
// alone is what makes "put a.txt /root" fail with an SFTP error that says
// nothing about the destination being a directory.
func (c *SFTPClient) PlanUpload(localPath, remotePath string) (*Plan, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("local path: %w", err)
	}

	plan := &Plan{Direction: Upload}
	if info.IsDir() {
		if err := planUploadDir(plan, localPath, remoteDirRoot(remotePath)); err != nil {
			return nil, err
		}
		return plan, nil
	}

	// An empty path, a trailing slash and an existing directory all say the
	// same thing: the file goes inside, under its own name.
	dest := remotePath
	if dest == "" || strings.HasSuffix(dest, "/") || c.isRemoteDir(dest) {
		dest = path.Join(dest, filepath.Base(localPath))
	}
	plan.Items = append(plan.Items, TransferItem{Source: localPath, Dest: dest, Size: info.Size()})
	plan.Bytes = info.Size()
	return plan, nil
}

// remoteDirRoot is the remote directory a local directory's contents are copied
// into, given the path the user named.
//
// Trimming the trailing slash cannot be allowed to empty an explicit root: "/"
// names the filesystem root, and reading it as "" would send the whole transfer
// to the login directory, where it lands looking like a success and has to be
// hunted for. Only a path that named nothing at all means the working directory,
// which is what an empty path gives way to — path.Join would otherwise turn it
// into a relative one.
func remoteDirRoot(remotePath string) string {
	if root := strings.TrimRight(remotePath, "/"); root != "" {
		return root
	}
	if remotePath != "" {
		// Nothing but slashes: the root, however it was spelled.
		return "/"
	}
	return "."
}

// localDirRoot is remoteDirRoot's counterpart for a local destination, where the
// separator is the platform's and a volume name has to survive the trim.
//
// "C:\" trimmed is "C:", which is not the drive root but the current directory
// on that drive — somewhere else entirely, and again not somewhere the user
// asked for.
func localDirRoot(localPath string) string {
	sep := string(os.PathSeparator)
	if root := strings.TrimRight(localPath, sep); root != "" {
		return root
	}
	if localPath != "" {
		if volume := filepath.VolumeName(localPath); volume != "" {
			return volume + sep
		}
		return sep
	}
	return "."
}

// isRemoteDir reports whether a remote path is an existing directory.
//
// A path that cannot be stat'ed is not one. Whatever is wrong with it is better
// said by the copy that follows than guessed at here: a path that merely does
// not exist yet is the ordinary case of a file being created.
func (c *SFTPClient) isRemoteDir(remote string) bool {
	info, err := c.Stat(remote)
	return err == nil && info.IsDir()
}

// isLocalDir is isRemoteDir's local counterpart, for the destination of a
// download.
func isLocalDir(local string) bool {
	info, err := os.Stat(local)
	return err == nil && info.IsDir()
}

// planUploadDir walks a local directory, recording the remote directories to
// create and the files to send under them.
func planUploadDir(plan *Plan, localDir, remoteDir string) error {
	plan.Dirs = append(plan.Dirs, remoteDir)

	entries, err := os.ReadDir(localDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", localDir, err)
	}
	for _, entry := range entries {
		local := filepath.Join(localDir, entry.Name())
		remote := path.Join(remoteDir, entry.Name())

		// A symlink is resolved only far enough to see what it points at. One
		// that names a regular file is copied by value; one that names a
		// directory is left alone, because following it would let a link
		// pointing back up the tree recurse forever, and one that resolves to
		// nothing is passed over rather than failing the whole transfer.
		info, err := os.Stat(local)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if err := planUploadDir(plan, local, remote); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			// Sockets, devices and named pipes have no bytes to send.
			continue
		}
		plan.Items = append(plan.Items, TransferItem{Source: local, Dest: remote, Size: info.Size()})
		plan.Bytes += info.Size()
	}
	return nil
}

// PlanDownload measures a remote path against where it is going.
//
// It is a method because the measurement happens over the connection: uploads
// walk the local filesystem, downloads have to walk the host.
func (c *SFTPClient) PlanDownload(remotePath, localPath string) (*Plan, error) {
	info, err := c.Stat(remotePath)
	if err != nil {
		return nil, fmt.Errorf("remote path: %w", err)
	}

	plan := &Plan{Direction: Download}
	if info.IsDir() {
		if err := c.planDownloadDir(plan, remotePath, localDirRoot(localPath)); err != nil {
			return nil, err
		}
		return plan, nil
	}

	// As in PlanUpload: empty, trailing separator and an existing directory all
	// mean the same thing, so "get host:/etc/hosts /tmp" lands in /tmp/hosts
	// rather than trying to open /tmp for writing.
	dest := localPath
	if dest == "" || strings.HasSuffix(dest, string(os.PathSeparator)) || isLocalDir(dest) {
		dest = filepath.Join(dest, path.Base(remotePath))
	}
	plan.Items = append(plan.Items, TransferItem{Source: remotePath, Dest: dest, Size: info.Size()})
	plan.Bytes = info.Size()
	return plan, nil
}

// planDownloadDir walks a remote directory, recording the local directories to
// create and the files to bring back under them.
func (c *SFTPClient) planDownloadDir(plan *Plan, remoteDir, localDir string) error {
	plan.Dirs = append(plan.Dirs, localDir)

	// ReadDir carries each entry's attributes, so the type and the size are
	// already in hand: measuring a tree costs one round trip per directory
	// rather than one per file.
	entries, err := c.ReadDir(remoteDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", remoteDir, err)
	}
	for _, entry := range entries {
		remote := path.Join(remoteDir, entry.Name())
		local := filepath.Join(localDir, entry.Name())

		if entry.IsDir() {
			if entry.Mode()&os.ModeSymlink != 0 {
				continue // see planUploadDir: a link to a directory is not followed
			}
			if err := c.planDownloadDir(plan, remote, local); err != nil {
				return err
			}
			continue
		}
		// A server that answers without attributes reports no type at all,
		// which reads as a regular file of no size. That is the right guess:
		// the copy itself is what decides whether the bytes were really there.
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			continue
		}
		plan.Items = append(plan.Items, TransferItem{Source: remote, Dest: local, Size: entry.Size()})
		plan.Bytes += entry.Size()
	}
	return nil
}

// Run carries out plan, reporting progress as it goes.
//
// A cancelled context stops the transfer at the next packet rather than in the
// middle of a write: both directions copy through pkg/sftp, which moves a file in
// MaxPacket-sized requests, and the wrappers below check the context on every one
// of them.
func (c *SFTPClient) Run(ctx context.Context, plan *Plan, progress ProgressFunc) error {
	if plan == nil {
		return nil
	}
	if err := c.makeDirs(plan); err != nil {
		return err
	}

	state := &runState{
		plan:    plan,
		report:  throttle(progress, progressInterval),
		started: time.Now(),
	}
	// The totals go out before the first byte, so a renderer has its denominator
	// from the start. Without them a bar cannot say how far along it is, and a
	// transfer of one large file would show nothing at all until it had finished.
	state.send(Progress{})

	for _, item := range plan.Items {
		if err := ctx.Err(); err != nil {
			return err
		}

		// The two directions differ in more than which end is which: a download can
		// be put in place at the end and an upload cannot, so they are separate
		// copies rather than one loop with the ends swapped.
		var err error
		if plan.Direction == Download {
			err = c.copyDown(ctx, item, state)
		} else {
			err = c.copyUp(ctx, item, state)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// makeDirs creates the directories the plan needs, on whichever end the
// direction puts them.
func (c *SFTPClient) makeDirs(plan *Plan) error {
	for _, dir := range plan.Dirs {
		if dir == "" || dir == "." || dir == "/" {
			continue
		}
		var err error
		if plan.Direction == Download {
			err = os.MkdirAll(dir, 0o755)
		} else {
			err = c.MkdirAll(dir)
		}
		if err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// copyDown brings one remote file back and only then puts it in place.
//
// The bytes land in a file beside the destination and the destination is renamed
// over at the end, so a download that fails or is cancelled leaves it as it found
// it. Otherwise an interrupted transfer leaves a file with the right name and half
// the contents, which is indistinguishable from a complete one and is read as one.
func (c *SFTPClient) copyDown(ctx context.Context, item TransferItem, state *runState) error {
	// A local parent is created here rather than during planning: downloading
	// one file into a directory that does not exist yet is an ordinary request.
	// The remote side gets no such help — a remote directory that is missing is
	// more likely to be a typo than a request to create a tree.
	if parent := filepath.Dir(item.Dest); parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", parent, err)
		}
	}

	source, err := c.Open(item.Source)
	if err != nil {
		return fmt.Errorf("open %s: %w", item.Source, err)
	}
	defer func() { _ = source.Close() }()

	partial := item.Dest + partialSuffix
	dest, err := os.Create(partial)
	if err != nil {
		return fmt.Errorf("create %s: %w", partial, err)
	}

	// *sftp.File implements io.WriterTo, which is what puts this copy on pkg/sftp's
	// concurrent path: it requests chunks of the file in parallel and hands them to
	// the writer in order. Copying the bytes through a buffer here would take that
	// away and cost a round trip per packet.
	sink := &copySink{file: dest, ctx: ctx, item: item, state: state}
	if _, err := io.Copy(sink, source); err != nil {
		_ = dest.Close()
		_ = os.Remove(partial)
		return err
	}

	// A local write is only guaranteed to have reached the file once it is closed,
	// so the rename waits for it. The rename itself is the one step that changes
	// what the user sees, and it either happens or it does not.
	if err := dest.Close(); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("close %s: %w", partial, err)
	}
	if err := os.Rename(partial, item.Dest); err != nil {
		_ = os.Remove(partial)
		return fmt.Errorf("move %s into place: %w", item.Dest, err)
	}
	state.finish(item.Source, sink.moved, item.Size)
	return nil
}

// copyUp sends one local file to the host.
//
// The remote file is created under its real name: renaming on the far side is not
// something every SFTP server does the same way, and it is not worth the risk for
// a failure path. So a copy that fails has to take the half-written file back with
// it — a leftover under the right name is worse than no file at all, because the
// next run finds it and calls the transfer done.
func (c *SFTPClient) copyUp(ctx context.Context, item TransferItem, state *runState) error {
	file, err := os.Open(item.Source)
	if err != nil {
		return fmt.Errorf("open %s: %w", item.Source, err)
	}
	defer func() { _ = file.Close() }()

	dest, err := c.Create(item.Dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", item.Dest, err)
	}

	// *sftp.File implements io.ReaderFrom, which is what puts this copy on
	// pkg/sftp's concurrent path — provided the source can say how much is left
	// to send, which copySource does.
	source := &copySource{file: file, ctx: ctx, item: item, state: state, size: item.Size}
	if _, err := io.Copy(dest, source); err != nil {
		_ = dest.Close()
		_ = c.Remove(item.Dest)
		return err
	}

	// The writer is closed here rather than deferred: for SFTP the close is a
	// round trip, and it is the error that says whether the bytes really landed.
	if err := dest.Close(); err != nil {
		_ = c.Remove(item.Dest)
		return fmt.Errorf("close %s: %w", item.Dest, err)
	}
	state.finish(item.Source, source.moved, item.Size)
	return nil
}

// copySink is the local end of a download: the file the bytes land in, which
// counts them and reports them as they arrive.
//
// A failure out of the file is labelled here, where the path is known. One from
// the far end is left as the SFTP layer phrased it: the caller already names both
// ends of the transfer, so it does not need saying twice.
type copySink struct {
	file  io.Writer
	ctx   context.Context
	item  TransferItem
	state *runState
	moved int64
}

func (s *copySink) Write(p []byte) (int, error) {
	// With pkg/sftp doing the reading, this is the only point the copy passes
	// through, so it is where a cancelled transfer has to be stopped.
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}

	n, err := s.file.Write(p)
	if n > 0 {
		s.moved += int64(n)
		s.state.chunk(s.item.Source, int64(n), s.moved, s.item.Size)
	}
	if err != nil {
		return n, fmt.Errorf("write %s: %w", s.item.Dest, err)
	}
	return n, nil
}

// copySource is the local end of an upload: the file the bytes come from, which
// counts them as they leave.
type copySource struct {
	file  io.Reader
	ctx   context.Context
	item  TransferItem
	state *runState
	moved int64
	// size is the whole file, from the plan's measurement.
	size int64
}

func (s *copySource) Read(p []byte) (int, error) {
	// See copySink.Write: the copy runs inside pkg/sftp, so the check belongs on
	// the side the transfer itself owns.
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}

	n, err := s.file.Read(p)
	if n > 0 {
		s.moved += int64(n)
		s.state.chunk(s.item.Source, int64(n), s.moved, s.item.Size)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("read %s: %w", s.item.Source, err)
	}
	return n, err
}

// Size is how much of the file is still to come. pkg/sftp reads it once, to size
// its concurrency, and treats a reader it cannot measure as a reason to write the
// file one packet at a time — which for a large file over a slow link is the
// difference this exists for.
func (s *copySource) Size() int64 { return s.size - s.moved }

// runState turns a stream of chunk counts into the reports a renderer wants.
type runState struct {
	plan     *Plan
	report   ProgressFunc
	started  time.Time
	moved    int64
	finished int
}

// chunk reports bytes that have just landed. n is what this chunk added, which
// is what the running total advances by.
func (s *runState) chunk(file string, n, moved, total int64) {
	s.moved += n
	s.send(Progress{File: file, FileDone: moved, FileTotal: total})
}

// finish reports a file as done and counts it, so the overall bar keeps up with
// files too small to produce an intermediate report.
func (s *runState) finish(file string, moved, total int64) {
	s.finished++
	s.send(Progress{File: file, FileDone: moved, FileTotal: total, Done: true})
}

// send fills in the running totals, the rate and the ETA, then hands the report
// on. The rate is measured from the start of the transfer rather than from the
// last report, so it does not swing with the reporting interval.
func (s *runState) send(p Progress) {
	if s.report == nil {
		return
	}
	p.TotalDone = s.moved
	p.TotalBytes = s.plan.Bytes
	p.FilesTotal = s.plan.Files()
	p.FilesDone = s.finished

	if elapsed := time.Since(s.started).Seconds(); elapsed > 0 {
		p.Rate = float64(s.moved) / elapsed
		if p.Rate > 0 && s.plan.Bytes > s.moved {
			p.ETA = time.Duration(float64(s.plan.Bytes-s.moved) / p.Rate * float64(time.Second))
		}
	}
	s.report(p)
}
