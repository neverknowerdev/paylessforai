package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
)

type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Name   string `json:"name,omitempty"`
}

type Manifest struct {
	Schema                int    `json:"schema"`
	Channel               string `json:"channel"`
	Version               string `json:"version"`
	Commit                string `json:"commit"`
	PublishedAt           string `json:"published_at"`
	MinSupervisorProtocol int    `json:"min_supervisor_protocol"`
	SchemaCompatibility   struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"schema_compatibility"`
	Artifacts []Artifact `json:"artifacts"`
}

func (m Manifest) ArtifactForCurrentPlatform() (Artifact, bool) {
	for _, item := range m.Artifacts {
		if item.OS == runtime.GOOS && item.Arch == runtime.GOARCH {
			return item, true
		}
	}
	return Artifact{}, false
}

func (m Manifest) CanonicalBytes() ([]byte, error) {
	copy := m
	return json.Marshal(copy)
}

func VerifyManifest(m Manifest, signature []byte, allowUnsigned bool) error {
	if m.Schema != 1 || strings.TrimSpace(m.Version) == "" || len(m.Artifacts) == 0 {
		return errors.New("invalid update manifest")
	}
	if m.MinSupervisorProtocol > 1 {
		return fmt.Errorf("manifest requires supervisor protocol %d", m.MinSupervisorProtocol)
	}
	if _, ok := m.ArtifactForCurrentPlatform(); !ok {
		return fmt.Errorf("manifest has no %s/%s artifact", runtime.GOOS, runtime.GOARCH)
	}
	if len(signature) == 0 && allowUnsigned {
		return nil
	}
	if len(signature) == 0 {
		return errors.New("update manifest signature is missing")
	}
	key, err := hex.DecodeString(strings.TrimSpace(buildinfo.UpdatePublicKey))
	if err != nil || len(key) != ed25519.PublicKeySize {
		return errors.New("update verification key is unavailable")
	}
	contents, err := m.CanonicalBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(key), contents, signature) {
		return errors.New("update manifest signature is invalid")
	}
	return nil
}

func VerifyArtifact(r io.Reader, expectedSize int64, expectedSHA string) ([]byte, error) {
	if expectedSize <= 0 || expectedSize > 512<<20 {
		return nil, errors.New("invalid update artifact size")
	}
	limited := io.LimitReader(r, expectedSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != expectedSize {
		return nil, fmt.Errorf("artifact size mismatch: got %d want %d", len(data), expectedSize)
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expectedSHA) {
		return nil, errors.New("artifact checksum mismatch")
	}
	return data, nil
}

func ExtractArtifact(data []byte, destination string) (string, error) {
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", err
	}
	if strings.HasSuffix(strings.ToLower(destination), ".zip") {
		return "", errors.New("invalid destination")
	}
	if len(data) >= 4 && data[0] == 'P' && data[1] == 'K' {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", err
		}
		return extractZip(reader, destination)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var executable string
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		path, err := safeArchivePath(destination, header.Name)
		if err != nil {
			return "", err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return "", err
			}
			file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
			if err != nil {
				return "", err
			}
			_, copyErr := io.CopyN(file, tr, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			if executable == "" && strings.HasPrefix(filepath.Base(path), "paylessforai-app") {
				executable = path
			}
		default:
			return "", errors.New("unsupported archive entry")
		}
	}
	if executable == "" {
		return "", errors.New("archive does not contain paylessforai-app")
	}
	return executable, nil
}

func extractZip(reader *zip.Reader, destination string) (string, error) {
	var executable string
	for _, item := range reader.File {
		path, err := safeArchivePath(destination, item.Name)
		if err != nil {
			return "", err
		}
		if item.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		input, err := item.Open()
		if err != nil {
			return "", err
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			input.Close()
			return "", err
		}
		_, copyErr := io.Copy(output, input)
		input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if executable == "" && strings.HasPrefix(filepath.Base(path), "paylessforai-app") {
			executable = path
		}
	}
	if executable == "" {
		return "", errors.New("archive does not contain paylessforai-app")
	}
	return executable, nil
}

func safeArchivePath(root, name string) (string, error) {
	name = filepath.Clean(name)
	if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return "", errors.New("archive path traversal")
	}
	path := filepath.Join(root, name)
	if filepath.Dir(path) != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", errors.New("archive path escapes destination")
	}
	return path, nil
}
