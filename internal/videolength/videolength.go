// Package videolength reads how long a video lasts from its headers alone:
// Matroska (.mkv, .webm) and MP4 (.mp4, .m4v, .mov). A few kilobytes are
// read, never the video itself, and nothing is written.
package videolength

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"time"
)

// ErrUnknown: the file isn't one this package reads, or says no length.
var ErrUnknown = errors.New("videolength: no length in this file")

// maxSkip bounds how far into a file the length is looked for: headers come
// first, and a damaged file mustn't make it read gigabytes.
const maxSkip = 64 << 20

// Of returns how long the video at path lasts.
func Of(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return Read(f, info.Size())
}

// Read returns how long the video in r (size bytes) lasts.
func Read(r io.ReaderAt, size int64) (time.Duration, error) {
	head := make([]byte, 8)
	if _, err := r.ReadAt(head, 0); err != nil {
		return 0, ErrUnknown
	}
	if binary.BigEndian.Uint32(head) == 0x1A45DFA3 {
		return matroska(r, size)
	}
	if string(head[4:8]) == "ftyp" || string(head[4:8]) == "moov" || string(head[4:8]) == "free" || string(head[4:8]) == "mdat" {
		return mp4(r, size)
	}
	return 0, ErrUnknown
}

// Matroska: EBML elements, an ID then a size, both of variable length.

const (
	idEBML          = 0x1A45DFA3
	idSegment       = 0x18538067
	idInfo          = 0x1549A966
	idTimecodeScale = 0x2AD7B1
	idDuration      = 0x4489
	idCluster       = 0x1F43B675
)

// vint reads a variable-length number at off: its value (the length marker
// kept for an ID, dropped for a size), its length, and whether a size is
// unknown (all ones).
func vint(r io.ReaderAt, off int64, keepMarker bool) (value uint64, n int, unknown bool, err error) {
	first := make([]byte, 1)
	if _, err := r.ReadAt(first, off); err != nil {
		return 0, 0, false, err
	}
	n = 1
	for mask := byte(0x80); n <= 8 && first[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 8 {
		return 0, 0, false, ErrUnknown
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, off); err != nil {
		return 0, 0, false, err
	}
	if !keepMarker {
		buf[0] &= 0xFF >> n
	}
	ones := true
	for i, b := range buf {
		value = value<<8 | uint64(b)
		full := byte(0xFF)
		if i == 0 {
			full = 0xFF >> n
		}
		ones = ones && b&full == full
	}
	return value, n, ones && !keepMarker, nil
}

func matroska(r io.ReaderAt, size int64) (time.Duration, error) {
	off := int64(0)
	end := min(size, maxSkip)
	for off < end {
		id, idLen, _, err := vint(r, off, true)
		if err != nil {
			return 0, ErrUnknown
		}
		length, sizeLen, unknown, err := vint(r, off+int64(idLen), false)
		if err != nil {
			return 0, ErrUnknown
		}
		body := off + int64(idLen+sizeLen)
		switch id {
		case idSegment:
			off = body // its children follow
			continue
		case idInfo:
			if unknown || length > 1<<20 {
				return 0, ErrUnknown
			}
			return matroskaInfo(r, body, body+int64(length))
		case idCluster:
			return 0, ErrUnknown // the video began, and no Info before it
		}
		if unknown {
			return 0, ErrUnknown
		}
		off = body + int64(length)
	}
	return 0, ErrUnknown
}

func matroskaInfo(r io.ReaderAt, off, end int64) (time.Duration, error) {
	scale := uint64(1_000_000) // nanoseconds a tick, Matroska's default
	duration := -1.0
	for off < end {
		id, idLen, _, err := vint(r, off, true)
		if err != nil {
			return 0, ErrUnknown
		}
		length, sizeLen, _, err := vint(r, off+int64(idLen), false)
		if err != nil || length > 8 && (id == idTimecodeScale || id == idDuration) {
			return 0, ErrUnknown
		}
		body := off + int64(idLen+sizeLen)
		switch id {
		case idTimecodeScale:
			buf := make([]byte, length)
			if _, err := r.ReadAt(buf, body); err != nil {
				return 0, ErrUnknown
			}
			scale = 0
			for _, b := range buf {
				scale = scale<<8 | uint64(b)
			}
		case idDuration:
			buf := make([]byte, length)
			if _, err := r.ReadAt(buf, body); err != nil {
				return 0, ErrUnknown
			}
			switch length {
			case 4:
				duration = float64(math.Float32frombits(binary.BigEndian.Uint32(buf)))
			case 8:
				duration = math.Float64frombits(binary.BigEndian.Uint64(buf))
			default:
				return 0, ErrUnknown
			}
		}
		off = body + int64(length)
	}
	return checked(duration * float64(scale))
}

// MP4: boxes, a 32-bit size then a type; size 1 means a 64-bit size
// follows, 0 that the box runs to the end.

func mp4(r io.ReaderAt, size int64) (time.Duration, error) {
	moov, moovEnd, ok := findBox(r, 0, size, "moov")
	if !ok {
		return 0, ErrUnknown
	}
	mvhd, mvhdEnd, ok := findBox(r, moov, moovEnd, "mvhd")
	if !ok || mvhdEnd-mvhd < 20 {
		return 0, ErrUnknown
	}
	buf := make([]byte, min(mvhdEnd-mvhd, 32))
	if _, err := r.ReadAt(buf, mvhd); err != nil {
		return 0, ErrUnknown
	}
	var timescale uint32
	var units uint64
	if buf[0] == 1 { // version 1: 64-bit times
		if len(buf) < 32 {
			return 0, ErrUnknown
		}
		timescale = binary.BigEndian.Uint32(buf[20:24])
		units = binary.BigEndian.Uint64(buf[24:32])
	} else {
		timescale = binary.BigEndian.Uint32(buf[12:16])
		units = uint64(binary.BigEndian.Uint32(buf[16:20]))
	}
	if timescale == 0 {
		return 0, ErrUnknown
	}
	return checked(float64(units) / float64(timescale) * 1e9)
}

// findBox finds the box of that type among those from off to end: where its
// body starts and ends.
func findBox(r io.ReaderAt, off, end int64, kind string) (int64, int64, bool) {
	head := make([]byte, 16)
	for boxes := 0; off+8 <= end && boxes < 10_000; boxes++ {
		if _, err := r.ReadAt(head[:8], off); err != nil {
			return 0, 0, false
		}
		length := int64(binary.BigEndian.Uint32(head[:4]))
		body := off + 8
		switch length {
		case 0:
			length = end - off
		case 1:
			if _, err := r.ReadAt(head[8:16], off+8); err != nil {
				return 0, 0, false
			}
			length = int64(binary.BigEndian.Uint64(head[8:16]))
			body = off + 16
		}
		if length < body-off || off+length > end {
			return 0, 0, false
		}
		if string(head[4:8]) == kind {
			return body, off + length, true
		}
		off += length
	}
	return 0, 0, false
}

// checked turns nanoseconds into a length, if it's one a video can have
// (up to a day).
func checked(ns float64) (time.Duration, error) {
	if math.IsNaN(ns) || ns <= 0 || ns > float64(24*time.Hour) {
		return 0, ErrUnknown
	}
	return time.Duration(ns), nil
}
