package apt

import (
	"bytes"
	"crypto/md5"  // #nosec G501 - MD5 required for APT repository compatibility
	"crypto/sha1" // #nosec G505 - SHA1 required for APT repository compatibility
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"path"

	"github.com/cockroachdb/errors"
)

// Checksums holds all checksum values for a file.
// A nil value for any checksum means it's not available or not required.
type Checksums struct {
	MD5    []byte // nil means no MD5 checksum to be checked
	SHA1   []byte // nil means no SHA1 checksum to be checked
	SHA256 []byte // nil means no SHA256 checksum to be checked
	SHA512 []byte // nil means no SHA512 checksum to be checked
}

// FileInfo is a set of meta data of a file.
type FileInfo struct {
	path      string
	size      uint64
	checksums Checksums
}

// Same returns true if t has the same checksum values.
func (fi *FileInfo) Same(t *FileInfo) bool {
	if fi == t {
		return true
	}
	if fi.path != t.path {
		return false
	}
	if fi.size != t.size {
		return false
	}
	if fi.checksums.MD5 != nil && !bytes.Equal(fi.checksums.MD5, t.checksums.MD5) {
		return false
	}
	if fi.checksums.SHA1 != nil && !bytes.Equal(fi.checksums.SHA1, t.checksums.SHA1) {
		return false
	}
	if fi.checksums.SHA256 != nil && !bytes.Equal(fi.checksums.SHA256, t.checksums.SHA256) {
		return false
	}
	if fi.checksums.SHA512 != nil && !bytes.Equal(fi.checksums.SHA512, t.checksums.SHA512) {
		return false
	}
	return true
}

// Path returns the indentifying path string of the file.
func (fi *FileInfo) Path() string {
	return fi.path
}

// Size returns the number of bytes of the file body.
func (fi *FileInfo) Size() uint64 {
	return fi.size
}

// HasChecksum returns true if fi has checksums.
func (fi *FileInfo) HasChecksum() bool {
	return fi.checksums.MD5 != nil
}

// CalcChecksums calculates checksums and stores them in fi.
func (fi *FileInfo) CalcChecksums(data []byte) {
	md5sum := md5.Sum(data)   // #nosec G401 - MD5 required for APT repository compatibility
	sha1sum := sha1.Sum(data) // #nosec G401 - SHA1 required for APT repository compatibility
	sha256sum := sha256.Sum256(data)
	sha512sum := sha512.Sum512(data)
	fi.size = uint64(len(data))
	fi.checksums.MD5 = md5sum[:]
	fi.checksums.SHA1 = sha1sum[:]
	fi.checksums.SHA256 = sha256sum[:]
	fi.checksums.SHA512 = sha512sum[:]
}

// AddPrefix creates a new FileInfo by prepending prefix to the path.
func (fi *FileInfo) AddPrefix(prefix string) *FileInfo {
	newFI := *fi
	newFI.path = path.Join(path.Clean(prefix), fi.path)
	return &newFI
}

// MD5SumPath returns the filepath for "by-hash" with md5 checksum.
// If fi has no checksum, an empty string will be returned.
func (fi *FileInfo) MD5SumPath() string {
	if fi.checksums.MD5 == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"MD5Sum",
		hex.EncodeToString(fi.checksums.MD5))
}

// SHA1Path returns the filepath for "by-hash" with sha1 checksum.
// If fi has no checksum, an empty string will be returned.
func (fi *FileInfo) SHA1Path() string {
	if fi.checksums.SHA1 == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"SHA1",
		hex.EncodeToString(fi.checksums.SHA1))
}

// SHA256Path returns the filepath for "by-hash" with sha256 checksum.
// If fi has no checksum, an empty string will be returned.
func (fi *FileInfo) SHA256Path() string {
	if fi.checksums.SHA256 == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"SHA256",
		hex.EncodeToString(fi.checksums.SHA256))
}

// SHA512Path returns the filepath for "by-hash" with sha512 checksum.
// If fi has no checksum, an empty string will be returned.
func (fi *FileInfo) SHA512Path() string {
	if fi.checksums.SHA512 == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"SHA512",
		hex.EncodeToString(fi.checksums.SHA512))
}

type fileInfoJSON struct {
	Path      string
	Size      int64
	MD5Sum    string
	SHA1Sum   string
	SHA256Sum string
	SHA512Sum string
}

// MarshalJSON implements json.Marshaler
func (fi *FileInfo) MarshalJSON() ([]byte, error) {
	var fij fileInfoJSON
	fij.Path = fi.path
	if fi.size > math.MaxInt64 {
		return nil, errors.Newf("file size %d exceeds maximum int64 value", fi.size)
	}
	fij.Size = int64(fi.size)
	if fi.checksums.MD5 != nil {
		fij.MD5Sum = hex.EncodeToString(fi.checksums.MD5)
	}
	if fi.checksums.SHA1 != nil {
		fij.SHA1Sum = hex.EncodeToString(fi.checksums.SHA1)
	}
	if fi.checksums.SHA256 != nil {
		fij.SHA256Sum = hex.EncodeToString(fi.checksums.SHA256)
	}
	if fi.checksums.SHA512 != nil {
		fij.SHA512Sum = hex.EncodeToString(fi.checksums.SHA512)
	}
	return json.Marshal(&fij)
}

// UnmarshalJSON implements json.Unmarshaler
func (fi *FileInfo) UnmarshalJSON(data []byte) error {
	var fij fileInfoJSON
	if err := json.Unmarshal(data, &fij); err != nil {
		return err
	}
	fi.path = fij.Path
	if fij.Size < 0 {
		return errors.Newf("negative file size %d not allowed", fij.Size)
	}
	fi.size = uint64(fij.Size)
	if fij.MD5Sum != "" {
		md5sum, err := hex.DecodeString(fij.MD5Sum)
		if err != nil {
			return errors.Wrap(err, "UnmarshalJSON MD5Sum for "+fij.Path)
		}
		fi.checksums.MD5 = md5sum
	}
	if fij.SHA1Sum != "" {
		sha1sum, err := hex.DecodeString(fij.SHA1Sum)
		if err != nil {
			return errors.Wrap(err, "UnmarshalJSON SHA1Sum for "+fij.Path)
		}
		fi.checksums.SHA1 = sha1sum
	}
	if fij.SHA256Sum != "" {
		sha256sum, err := hex.DecodeString(fij.SHA256Sum)
		if err != nil {
			return errors.Wrap(err, "UnmarshalJSON SHA256Sum for "+fij.Path)
		}
		fi.checksums.SHA256 = sha256sum
	}
	if fij.SHA512Sum != "" {
		sha512sum, err := hex.DecodeString(fij.SHA512Sum)
		if err != nil {
			return errors.Wrap(err, "UnmarshalJSON SHA512Sum for "+fij.Path)
		}
		fi.checksums.SHA512 = sha512sum
	}
	return nil
}

// CopyWithFileInfo copies from src to dst until either EOF is reached
// on src or an error occurs, and returns FileInfo calculated while copying.
func CopyWithFileInfo(dst io.Writer, src io.Reader, p string) (*FileInfo, error) {
	md5hash := md5.New()   // #nosec G401 - MD5 required for APT repository compatibility
	sha1hash := sha1.New() // #nosec G401 - SHA1 required for APT repository compatibility
	sha256hash := sha256.New()
	sha512hash := sha512.New()

	w := io.MultiWriter(md5hash, sha1hash, sha256hash, sha512hash, dst)
	n, err := io.Copy(w, src)
	if err != nil {
		return nil, err
	}

	return &FileInfo{
		path: p,
		size: uint64(n), // #nosec G115 - io.Copy returns int64, conversion is safe as n >= 0
		checksums: Checksums{
			MD5:    md5hash.Sum(nil),
			SHA1:   sha1hash.Sum(nil),
			SHA256: sha256hash.Sum(nil),
			SHA512: sha512hash.Sum(nil),
		},
	}, nil
}

// MakeFileInfoNoChecksum constructs a FileInfo without calculating checksums.
func MakeFileInfoNoChecksum(path string, size uint64) *FileInfo {
	return &FileInfo{
		path: path,
		size: size,
	}
}
