package inventory

import "github.com/overfin/overtide/internal/model"

func enrichmentReliable(state string) bool {
	return state == "reliable" || state == "not configured"
}

// valuationReady is whether Overfin's scores, the only values automatic
// removal plans with, are reliable (or Overfin isn't configured), so
// automatic removal may plan.
func (reliability Reliability) valuationReady() bool {
	return enrichmentReliable(reliability.Overfin)
}

func cloneMedia(in []model.Media) []model.Media {
	out := make([]model.Media, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Tags = append([]string(nil), in[i].Tags...)
		out[i].DownloadIDs = append([]string(nil), in[i].DownloadIDs...)
		out[i].Torrents = cloneTorrents(in[i].Torrents)
		out[i].RetentionValueReasons = append([]model.Reason(nil), in[i].RetentionValueReasons...)
		out[i].Seasons = cloneSeasons(in[i].Seasons)
	}
	return out
}

func cloneSeasons(in []model.Season) []model.Season {
	out := make([]model.Season, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].RetentionValueReasons = append([]model.Reason(nil), in[i].RetentionValueReasons...)
	}
	return out
}

func cloneTorrents(in []model.Torrent) []model.Torrent {
	out := make([]model.Torrent, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].TorrentValueReasons = append([]model.Reason(nil), in[i].TorrentValueReasons...)
		out[i].MediaItems = append([]model.MediaRef(nil), in[i].MediaItems...)
		out[i].FormerMediaItems = append([]model.MediaRef(nil), in[i].FormerMediaItems...)
		out[i].HardlinkKnownMediaItems = append([]model.MediaRef(nil), in[i].HardlinkKnownMediaItems...)
		out[i].HardlinkedMediaItems = append([]model.MediaRef(nil), in[i].HardlinkedMediaItems...)
		out[i].HardlinkedSeasons = append([]int(nil), in[i].HardlinkedSeasons...)
	}
	return out
}
