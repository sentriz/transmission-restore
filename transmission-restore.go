package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hekmon/transmissionrpc"
	bencode "github.com/jackpal/bencode-go"
)

var confTorrentsPath string
var confTransmissionHost string
var confTransmissionUsername string
var confTransmissionPassword string
var confTransmissionHTTPS bool
var confTransmissionPort int
var confMappings mappings
var confDryRun bool
var confWaitSecs int

var bt *transmissionrpc.Client

type mappings []mapping
type mapping struct {
	local, remote string
}

func (m *mappings) String() string { return "" }
func (m *mappings) Set(value string) error {
	parts := strings.SplitN(value, ";", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid mapping %q", value)
	}
	*m = append(*m, mapping{
		local:  parts[0],
		remote: parts[1],
	})
	return nil
}

func init() {
	flag.StringVar(&confTorrentsPath, "torrents-path", "", "path to torrent files")
	flag.StringVar(&confTransmissionHost, "transmission-host", "", "transmission rpc host")
	flag.StringVar(&confTransmissionUsername, "transmission-username", "", "transmission rpc username")
	flag.StringVar(&confTransmissionPassword, "transmission-password", "", "transmission rpc password")
	flag.BoolVar(&confTransmissionHTTPS, "transmission-https", false, "transmission rpc https")
	flag.IntVar(&confTransmissionPort, "transmission-port", 0, "transmission rpc port")
	flag.Var(&confMappings, "mapping", "local to transmission directory mapping (add many)")
	flag.BoolVar(&confDryRun, "dry-run", false, "do a dry run instead of adding to transmission")
	flag.IntVar(&confWaitSecs, "wait-secs", 0, "time to wait in seconds between uploading")
	flag.Parse()
}

func main() {
	var err error
	bt, err = transmissionrpc.New(confTransmissionHost, confTransmissionUsername, confTransmissionPassword,
		&transmissionrpc.AdvancedConfig{
			HTTPS: confTransmissionHTTPS,
			Port:  uint16(confTransmissionPort),
		},
	)
	if err != nil {
		log.Fatalf("error connecting to transmission: %v", err)
	}

	torrents, err := readTorrentFiles(confTorrentsPath)
	if err != nil {
		log.Fatalf("read torrent files of %q: %v", confTorrentsPath, err)
	}

	log.Printf("parsed %d torrent files\n", len(torrents))
	log.Printf("using %d mappings\n\n", len(confMappings))

	for _, mapping := range confMappings {
		if err := processMapping(torrents, mapping.local, mapping.remote); err != nil {
			log.Fatalf("error processing mapping %q -> %q: %v", mapping.local, mapping.remote, err)
		}
	}
}

type torrents map[string][]byte

type info struct {
	Announce     string "announce"
	CreatedBy    string "created by"
	CreationDate int    "creation date"
	Encoding     string "encoding"
	Info         struct {
		Files []struct {
			Length int      "length"
			Path   []string "path"
		} "files"
		Name        string "name"
		PieceLength int    "piece length"
		Pieces      string "pieces"
		Private     int    "private"
		Source      string "source"
	} "info"
}

func readTorrentFiles(dir string) (torrents, error) {
	torrents := torrents{}
	return torrents, iterDir(dir, func(entry fs.DirEntry, path string) error {
		if entry.IsDir() {
			return nil
		}
		file, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}

		var inf info
		if err := bencode.Unmarshal(bytes.NewReader(file), &inf); err != nil {
			return fmt.Errorf("unmarshal torrent: %w", err)
		}

		torrents[inf.Info.Name] = file
		return nil
	})
}

func processMapping(torrents torrents, dirLocal, dirRemote string) error {
	return iterDir(dirLocal, func(entry fs.DirEntry, _ string) error {
		data, ok := torrents[entry.Name()]
		if !ok {
			return nil
		}
		log.Printf("adding torrent to transmission\n\tname %q\n\tdir local %q\n\tdir remote %q\n\tlen %v\n",
			entry.Name(), dirLocal, dirRemote, len(data))
		if confDryRun {
			return nil
		}

		datab64 := base64.StdEncoding.EncodeToString(data)
		paused := true
		_, err := bt.TorrentAdd(&transmissionrpc.TorrentAddPayload{
			DownloadDir: &dirRemote,
			MetaInfo:    &datab64,
			Paused:      &paused,
		})
		if err != nil {
			return err
		}

		time.Sleep(time.Second * time.Duration(confWaitSecs))
		return nil
	})
}

func iterDir(dir string, cb func(entry fs.DirEntry, path string) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := cb(entry, path); err != nil {
			return fmt.Errorf("%q: %w", path, err)
		}
	}
	return nil
}
