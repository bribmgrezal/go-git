package packfile

import (
	"bufio"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"

	"github.com/go-git/go-git/v5/plumbing"
)

// Scanner reads and decodes packfile data from a reader, providing
// low-level access to the raw object entries within a packfile.
type Scanner struct {
	r     io.ReadSeeker
	br    *bufio.Reader
	hash  hash.Hash32
	count uint32
	read  int64
}

// NewScanner creates a new Scanner that reads from r.
func NewScanner(r io.ReadSeeker) *Scanner {
	return &Scanner{
		r:    r,
		br:   bufio.NewReader(r),
		hash: crc32.NewIEEE(),
	}
}

// Header reads and returns the packfile header, which contains the
// magic signature, version number, and total object count.
func (s *Scanner) Header() (version, objects uint32, err error) {
	sig := make([]byte, 4)
	if _, err = io.ReadFull(s.br, sig); err != nil {
		return 0, 0, fmt.Errorf("reading signature: %w", err)
	}
	if string(sig) != "PACK" {
		return 0, 0, ErrBadSignature
	}

	if err = binary.Read(s.br, binary.BigEndian, &version); err != nil {
		return 0, 0, fmt.Errorf("reading version: %w", err)
	}
	if version != 2 && version != 3 {
		return 0, 0, ErrUnsupportedVersion
	}

	if err = binary.Read(s.br, binary.BigEndian, &objects); err != nil {
		return 0, 0, fmt.Errorf("reading object count: %w", err)
	}

	s.count = objects
	return version, objects, nil
}

// NextObjectHeader reads the header of the next object in the packfile.
// It returns the object type, uncompressed size, and any reference hash
// (for OBJ_OFS_DELTA and OBJ_REF_DELTA types).
func (s *Scanner) NextObjectHeader() (typ plumbing.ObjectType, size int64, ref plumbing.Hash, offset int64, err error) {
	s.hash.Reset()

	// Read the type and size from the variable-length encoding.
	b, err := s.br.ReadByte()
	if err != nil {
		return 0, 0, plumbing.ZeroHash, 0, fmt.Errorf("reading object header byte: %w", err)
	}
	s.hash.Write([]byte{b})

	typ = plumbing.ObjectType((b >> 4) & 0x7)
	size = int64(b & 0xf)
	shift := uint(4)

	for b&0x80 != 0 {
		b, err = s.br.ReadByte()
		if err != nil {
			return 0, 0, plumbing.ZeroHash, 0, fmt.Errorf("reading size continuation: %w", err)
		}
		s.hash.Write([]byte{b})
		size |= int64(b&0x7f) << shift
		shift += 7
	}

	switch typ {
	case plumbing.OFSDeltaObject:
		offset, err = s.readNegativeOffset()
		if err != nil {
			return 0, 0, plumbing.ZeroHash, 0, err
		}
	case plumbing.REFDeltaObject:
		if _, err = io.ReadFull(s.br, ref[:]); err != nil {
			return 0, 0, plumbing.ZeroHash, 0, fmt.Errorf("reading ref delta hash: %w", err)
		}
		s.hash.Write(ref[:])
	}

	return typ, size, ref, offset, nil
}

// readNegativeOffset decodes a variable-length negative offset used in
// OBJ_OFS_DELTA objects to locate the base object.
func (s *Scanner) readNegativeOffset() (int64, error) {
	b, err := s.br.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("reading offset byte: %w", err)
	}
	s.hash.Write([]byte{b})

	var offset = int64(b & 0x7f)
	for b&0x80 != 0 {
		b, err = s.br.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("reading offset continuation: %w", err)
		}
		s.hash.Write([]byte{b})
		offset = ((offset + 1) << 7) | int64(b&0x7f)
	}

	return -offset, nil
}

// NextObject returns a reader for the compressed data of the current object.
// The caller must fully consume and close the reader before calling NextObjectHeader again.
func (s *Scanner) NextObject(w io.Writer) (n int64, crc32 uint32, err error) {
	zr, err := zlib.NewReader(io.TeeReader(s.br, s.hash))
	if err != nil {
		return 0, 0, fmt.Errorf("creating zlib reader: %w", err)
	}
	defer zr.Close()

	n, err = io.Copy(w, zr)
	if err != nil {
		return n, 0, fmt.Errorf("decompressing object: %w", err)
	}

	return n, s.hash.Sum32(), nil
}
