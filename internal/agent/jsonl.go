package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
)

var ErrNoUsage = errors.New("no usage records for this session")

type FileCursor struct {
	Offset int64 `json:"off"`
	Size   int64 `json:"size"`
	MTime  int64 `json:"mtime"`
}

func (c FileCursor) Unchanged(fi os.FileInfo) bool {
	return c.Size == fi.Size() && c.MTime == fi.ModTime().UnixNano()
}

func (c FileCursor) Behind(fi os.FileInfo) bool { return fi.Size() < c.Offset }

func ScanLines(ctx context.Context, path string, from int64, line func([]byte)) (FileCursor, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileCursor{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return FileCursor{}, err
	}
	size := fi.Size()
	if from < 0 || from > size {
		from = 0
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return FileCursor{}, err
	}
	r := bufio.NewReaderSize(io.LimitReader(f, size-from), 1<<20)
	offset := from
	for n := 1; ; n++ {
		raw, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return FileCursor{}, err
		}
		offset += int64(len(raw))
		line(raw)
		if n%1000 == 0 {
			if err := ctx.Err(); err != nil {
				return FileCursor{}, err
			}
		}
	}
	return FileCursor{Offset: offset, Size: size, MTime: fi.ModTime().UnixNano()}, nil
}

func EncodeCursor(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func DecodeCursor(s string, v any) bool {
	return s != "" && json.Unmarshal([]byte(s), v) == nil
}
