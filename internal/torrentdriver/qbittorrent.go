package torrentdriver

import (
	"context"
	"errors"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/qbittorrent"
)

// qbDriver is qBittorrent.
type qbDriver struct {
	name   string
	client *qbittorrent.Client
	ctx    context.Context
}

// QBittorrent is the driver of the qBittorrent at url; a key, or a
// username and password.
func QBittorrent(name, url, username, password, apiKey string) Driver {
	if name == "" {
		name = "qBittorrent"
	}
	return &qbDriver{name: name, client: qbittorrent.New(name, url, username, password, apiKey), ctx: context.Background()}
}

func (d *qbDriver) Name() string { return d.name }

func (d *qbDriver) WithContext(ctx context.Context) Driver {
	return &qbDriver{name: d.name, client: d.client.WithContext(ctx), ctx: ctx}
}

func (d *qbDriver) Torrents(previous map[string]model.Torrent, cursor int64) (map[string]model.Torrent, int64, error) {
	var torrents map[string]model.Torrent
	next := cursor
	err := retry(d.ctx, ReadWaits, func() (err error) {
		torrents, next, err = d.client.Sync(previous, cursor)
		return err
	})
	return torrents, next, err
}

func (d *qbDriver) Detail(hash string) (model.Torrent, error) {
	var torrent model.Torrent
	err := retry(d.ctx, ReadWaits, func() (err error) {
		torrent, err = d.client.Detail(hash)
		return err
	})
	if errors.Is(err, qbittorrent.ErrTorrentNotFound) {
		return model.Torrent{}, ErrNotFound
	}
	return torrent, err
}

func (d *qbDriver) Files(torrents map[string]model.Torrent) (map[string][]File, error) {
	var found map[string][]qbittorrent.File
	if err := retry(d.ctx, ReadWaits, func() (err error) {
		found, err = d.client.AllFiles(torrents)
		return err
	}); err != nil {
		return nil, err
	}
	out := make(map[string][]File, len(found))
	for hash, files := range found {
		list := make([]File, 0, len(files))
		for _, f := range files {
			list = append(list, File{Index: f.Index, Name: f.Name, Size: f.Size, Progress: f.Progress})
		}
		out[hash] = list
	}
	return out, nil
}

func (d *qbDriver) Roots(torrents map[string]model.Torrent) ([]Root, error) {
	var found []qbittorrent.RootPath
	if err := retry(d.ctx, ReadWaits, func() (err error) {
		found, err = d.client.StorageRoots(torrents)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]Root, 0, len(found))
	for _, r := range found {
		root := Root{Path: r.Path}
		if r.Purpose == qbittorrent.RootPurposeIncompleteDownloads {
			root.Purpose = PurposeIncompleteDownloads
		}
		out = append(out, root)
	}
	return out, nil
}

// Remove: qBittorrent answers a delete of a torrent it doesn't have like
// any other, so trying again after a reset, when the first may have gone
// through, is safe.
func (d *qbDriver) Remove(hash string, withData bool) error {
	return retry(d.ctx, Waits, func() error {
		if withData {
			return d.client.Delete(hash)
		}
		return d.client.DeleteKeepFiles(hash)
	})
}

func (d *qbDriver) NotClaimed(paths []string) error {
	return retry(d.ctx, ReadWaits, func() error { return d.client.VerifyPathsUnmanaged(paths) })
}

func (d *qbDriver) ClaimedByOthers(paths []string, except map[string]bool) ([]string, error) {
	var out []string
	err := retry(d.ctx, ReadWaits, func() error {
		var err error
		out, err = d.client.ClaimedByOthers(paths, except)
		return err
	})
	return out, err
}

// Check isn't tried again: it answers someone adding the service, who
// wants to know now.
func (d *qbDriver) Check() error { return d.client.Validate() }

// FileRoot is the folder a torrent's file names are relative to (as
// qBittorrent reports them, the only client so far).
func FileRoot(t model.Torrent, files []File) string {
	list := make([]qbittorrent.File, 0, len(files))
	for _, f := range files {
		list = append(list, qbittorrent.File{Index: f.Index, Name: f.Name, Size: f.Size, Progress: f.Progress})
	}
	return qbittorrent.TorrentFileRoot(t, list)
}
