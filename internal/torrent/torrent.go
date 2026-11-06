// Package torrent reads the list of files in a .torrent: it's what can be
// known of a release before downloading it.
package torrent

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// File is a file of a torrent. Path is relative to the torrent's folder.
type File struct {
	Path string
	Size int64
}

// maxDepth bounds how deep a .torrent nests: real ones are a few levels.
const maxDepth = 32

var errBad = errors.New("not a torrent")

// Files lists the files of a .torrent.
func Files(data []byte) ([]File, error) {
	parsed, rest, err := decode(data, 0)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errBad
	}
	root, ok := parsed.(map[string]any)
	if !ok {
		return nil, errBad
	}
	info, ok := root["info"].(map[string]any)
	if !ok {
		return nil, errBad
	}
	if list, ok := info["files"].([]any); ok {
		files := make([]File, 0, len(list))
		for _, raw := range list {
			entry, ok := raw.(map[string]any)
			if !ok {
				return nil, errBad
			}
			parts, ok := entry["path"].([]any)
			if !ok || len(parts) == 0 {
				return nil, errBad
			}
			names := make([]string, 0, len(parts))
			for _, part := range parts {
				name, ok := part.(string)
				if !ok {
					return nil, errBad
				}
				names = append(names, name)
			}
			size, ok := entry["length"].(int64)
			if !ok {
				return nil, errBad
			}
			files = append(files, File{Path: strings.Join(names, "/"), Size: size})
		}
		return files, nil
	}
	name, ok := info["name"].(string)
	if !ok {
		return nil, errBad
	}
	size, ok := info["length"].(int64)
	if !ok {
		return nil, errBad
	}
	return []File{{Path: name, Size: size}}, nil
}

// decode reads one bencoded value, and returns what's left after it.
func decode(data []byte, depth int) (any, []byte, error) {
	if depth > maxDepth || len(data) == 0 {
		return nil, nil, errBad
	}
	switch c := data[0]; {
	case c == 'i':
		end := bytes.IndexByte(data[:min(len(data), 32)], 'e')
		if end < 0 {
			return nil, nil, errBad
		}
		value, err := strconv.ParseInt(string(data[1:end]), 10, 64)
		if err != nil {
			return nil, nil, errBad
		}
		return value, data[end+1:], nil
	case c == 'l':
		data = data[1:]
		list := []any{}
		for len(data) > 0 && data[0] != 'e' {
			value, rest, err := decode(data, depth+1)
			if err != nil {
				return nil, nil, err
			}
			list, data = append(list, value), rest
		}
		if len(data) == 0 {
			return nil, nil, errBad
		}
		return list, data[1:], nil
	case c == 'd':
		data = data[1:]
		dict := map[string]any{}
		for len(data) > 0 && data[0] != 'e' {
			key, rest, err := decode(data, depth+1)
			if err != nil {
				return nil, nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, nil, errBad
			}
			value, rest, err := decode(rest, depth+1)
			if err != nil {
				return nil, nil, err
			}
			dict[name], data = value, rest
		}
		if len(data) == 0 {
			return nil, nil, errBad
		}
		return dict, data[1:], nil
	case c >= '0' && c <= '9':
		colon := bytes.IndexByte(data[:min(len(data), 32)], ':')
		if colon < 0 {
			return nil, nil, errBad
		}
		size, err := strconv.Atoi(string(data[:colon]))
		if err != nil || size < 0 || size > len(data)-colon-1 {
			return nil, nil, errBad
		}
		start := colon + 1
		return string(data[start : start+size]), data[start+size:], nil
	}
	return nil, nil, errBad
}
