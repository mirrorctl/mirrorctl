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

// FileInfo is a set of meta data of a file.
type FileInfo struct {
	path      string
	size      uint64
	md5sum    []byte // nil means no MD5 checksum to be checked.
	sha1sum   []byte // nil means no SHA1 ...
	sha256sum []byte // nil means no SHA256 ...
	sha512sum []byte // nil means no SHA512 ...
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
	if fi.md5sum != nil && !bytes.Equal(fi.md5sum, t.md5sum) {
		return false
	}
	if fi.sha1sum != nil && !bytes.Equal(fi.sha1sum, t.sha1sum) {
		return false
	}
	if fi.sha256sum != nil && !bytes.Equal(fi.sha256sum, t.sha256sum) {
		return false
	}
	if fi.sha512sum != nil && !bytes.Equal(fi.sha512sum, t.sha512sum) {
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
	return fi.md5sum != nil
}

// CalcChecksums calculates checksums and stores them in fi.
func (fi *FileInfo) CalcChecksums(data []byte) {
	md5sum := md5.Sum(data)   // #nosec G401 - MD5 required for APT repository compatibility
	sha1sum := sha1.Sum(data) // #nosec G401 - SHA1 required for APT repository compatibility
	sha256sum := sha256.Sum256(data)
	sha512sum := sha512.Sum512(data)
	fi.size = uint64(len(data))
	fi.md5sum = md5sum[:]
	fi.sha1sum = sha1sum[:]
	fi.sha256sum = sha256sum[:]
	fi.sha512sum = sha512sum[:]
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
	if fi.md5sum == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"MD5Sum",
		hex.EncodeToString(fi.md5sum))
}

// SHA1Path returns the filepath for "by-hash" with sha1 checksum.
// If fi has no checksum, an empty string will be returned.
func (fi *FileInfo) SHA1Path() string {
	if fi.sha1sum == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"SHA1",
		hex.EncodeToString(fi.sha1sum))
}

// SHA256Path returns the filepath for "by-hash" with sha256 checksum.
// If fi has no checksum, an empty string will be returned.
func (fi *FileInfo) SHA256Path() string {
	if fi.sha256sum == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"SHA256",
		hex.EncodeToString(fi.sha256sum))
}

// SHA512Path returns the filepath for "by-hash" with sha512 checksum.
// If fi has no checksum, an empty string will be returned.
func (fi *FileInfo) SHA512Path() string {
	if fi.sha512sum == nil {
		return ""
	}
	return path.Join(path.Dir(fi.path),
		"by-hash",
		"SHA512",
		hex.EncodeToString(fi.sha512sum))
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
	if fi.md5sum != nil {
		fij.MD5Sum = hex.EncodeToString(fi.md5sum)
	}
	if fi.sha1sum != nil {
		fij.SHA1Sum = hex.EncodeToString(fi.sha1sum)
	}
	if fi.sha256sum != nil {
		fij.SHA256Sum = hex.EncodeToString(fi.sha256sum)
	}
	if fi.sha512sum != nil {
		fij.SHA512Sum = hex.EncodeToString(fi.sha512sum)
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
		fi.md5sum = md5sum
	}
	if fij.SHA1Sum != "" {
		sha1sum, err := hex.DecodeString(fij.SHA1Sum)
		if err != nil {
			return errors.Wrap(err, "UnmarshalJSON SHA1Sum for "+fij.Path)
		}
		fi.sha1sum = sha1sum
	}
	if fij.SHA256Sum != "" {
		sha256sum, err := hex.DecodeString(fij.SHA256Sum)
		if err != nil {
			return errors.Wrap(err, "UnmarshalJSON SHA256Sum for "+fij.Path)
		}
		fi.sha256sum = sha256sum
	}
	if fij.SHA512Sum != "" {
		sha512sum, err := hex.DecodeString(fij.SHA512Sum)
		if err != nil {
			return errors.Wrap(err, "UnmarshalJSON SHA512Sum for "+fij.Path)
		}
		fi.sha512sum = sha512sum
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
		path:      p,
		size:      uint64(n), // #nosec G115 - io.Copy returns int64, conversion is safe as n >= 0
		md5sum:    md5hash.Sum(nil),
		sha1sum:   sha1hash.Sum(nil),
		sha256sum: sha256hash.Sum(nil),
		sha512sum: sha512hash.Sum(nil),
	}, nil
}

// MakeFileInfoNoChecksum constructs a FileInfo without calculating checksums.
func MakeFileInfoNoChecksum(path string, size uint64) *FileInfo {
	return &FileInfo{
		path: path,
		size: size,
	}
}
