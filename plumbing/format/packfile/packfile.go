// Package packfile implements encoding and decoding of git packfile format.
// Packfiles are used by git to store objects efficiently by delta compression.
package packfile

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/hash"
)

const (
	// Signature is the magic bytes at the beginning of a packfile.
	Signature = "PACK"

	// VersionSupported is the packfile format version this package supports.
	// Git has used version 2 since 2005; version 3 does not exist in the wild.
	VersionSupported uint32 = 2

	// HeaderSize is the size of the packfile header in bytes.
	// 4 bytes signature + 4 bytes version + 4 bytes object count
	HeaderSize = 12
)

// ObjectType represents the type of a packed object.
type ObjectType int8

const (
	ObjectCommit   ObjectType = 1
	ObjectTree     ObjectType = 2
	ObjectBlob     ObjectType = 3
	ObjectTag      ObjectType = 4
	ObjectOfsDelta ObjectType = 6
	ObjectRefDelta ObjectType = 7
)

// String returns a human-readable name for the object type.
func (t ObjectType) String() string {
	switch t {
	case ObjectCommit:
		return "commit"
	case ObjectTree:
		return "tree"
	case ObjectBlob:
		return "blob"
	case ObjectTag:
		return "tag"
	case ObjectOfsDelta:
		return "ofs-delta"
	case ObjectRefDelta:
		return "ref-delta"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

// IsBase returns true if the object type is a non-delta (base) object.
func (t ObjectType) IsBase() bool {
	return t == ObjectCommit || t == ObjectTree || t == ObjectBlob || t == ObjectTag
}

// IsDelta returns true if the object type is a delta object.
func (t ObjectType) IsDelta() bool {
	return t == ObjectOfsDelta || t == ObjectRefDelta
}

// Header represents the header of a packfile.
type Header struct {
	Signature [4]byte
	Version   uint32
	ObjectNum uint32
}

// Validate checks that the header contains expected magic bytes and a supported version.
func (h *Header) Validate() error {
	if string(h.Signature[:]) != Signature {
		return fmt.Errorf("invalid packfile signature: %q", h.Signature)
	}
	if h.Version != VersionSupported {
		return fmt.Errorf("unsupported packfile version: %d (expected %d)", h.Version, VersionSupported)
	}
	return nil
}

// ReadHeader reads and validates a packfile header from r.
func ReadHeader(r io.Reader) (*Header, error) {
	hdr := &Header{}
	if err := binary.Read(r, binary.BigEndian, hdr); err != nil {
		return nil, fmt.Errorf("reading packfile header: %w", err)
	}
	if err := hdr.Validate(); err != nil {
		return nil, err
	}
	return hdr, nil
}

// WriteHeader serialises the packfile header to w.
func WriteHeader(w io.Writer, objectCount uint32) error {
	hdr := Header{
		Version:   VersionSupported,
		ObjectNum: objectCount,
	}
	copy(hdr.Signature[:], Signature)
	return binary.Write(w, binary.BigEndian, hdr)
}

// ObjectHeader holds metadata about a single packed object entry.
type ObjectHeader struct {
	Type   ObjectType
	Length uint64 // uncompressed size

	// OffsetReference is the negative offset from the current position
	// to the base object (only valid for OFS_DELTA objects).
	OffsetReference int64

	// Reference is the hash of the base object (only valid for REF_DELTA objects).
	Reference plumbing.Hash
}

// Checksum computes the SHA-1 checksum over all bytes written to buf,
// appending the 20-byte digest and returning the complete packfil
