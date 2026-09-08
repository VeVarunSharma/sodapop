//go:build ignore

// Standard-library test fixture writer; production extraction is releasectl's job.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
)

type entry struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	File    string `json:"file"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
}

func run() (err error) {
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		return err
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	out, err := os.Create(os.Args[2])
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	var tw *tar.Writer
	var zw *zip.Writer
	var gz *gzip.Writer
	if path.Ext(os.Args[2]) == ".zip" {
		zw = zip.NewWriter(out)
	} else {
		gz = gzip.NewWriter(out)
		tw = tar.NewWriter(gz)
	}
	for _, entry := range entries {
		content := []byte(entry.Content)
		if entry.File != "" {
			content, err = os.ReadFile(entry.File)
			if err != nil {
				return err
			}
		}
		mode := int64(0644)
		if entry.Mode != "" {
			mode, err = strconv.ParseInt(entry.Mode, 8, 64)
			if err != nil {
				return err
			}
		}
		if zw != nil {
			header := &zip.FileHeader{Name: entry.Name, Method: zip.Deflate}
			header.SetMode(os.FileMode(mode))
			if entry.Type == "symlink" {
				header.SetMode(os.ModeSymlink | os.FileMode(mode))
			}
			writer, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}
			if _, err = writer.Write(content); err != nil {
				return err
			}
		} else {
			header := &tar.Header{Name: entry.Name, Mode: mode, Size: int64(len(content)), Typeflag: tar.TypeReg}
			if entry.Type == "symlink" {
				header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, entry.Content, 0
				content = nil
			}
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if _, err := tw.Write(content); err != nil {
				return err
			}
		}
	}
	if zw != nil {
		return zw.Close()
	}
	return errors.Join(tw.Close(), gz.Close())
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
