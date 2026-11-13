package chunkbackup

import (
	"io"
)

const (
	ChunkMin = 64 << 10
	ChunkAvg = 256 << 10
	ChunkMax = 1 << 20
)

var gear [256]uint64

func init() {
	var s uint64 = 0x9E3779B185EBCA87
	for i := range gear {
		s += 0x9E3779B97F4A7C15
		z := s
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		z = z ^ (z >> 31)
		if z == 0 {
			z = 0x9E3779B97F4A7C15
		}
		gear[i] = z
	}
}

type Chunker struct {
	r     io.Reader
	buf   []byte
	err   error
	maskS uint64
	maskL uint64
}

func NewChunker(r io.Reader) *Chunker {
	bits := 0
	for n := uint64(ChunkAvg); n > 1; n >>= 1 {
		bits++
	}
	return &Chunker{
		r:     r,
		buf:   make([]byte, 0, ChunkMax),
		maskS: (uint64(1) << (bits + 1)) - 1,
		maskL: (uint64(1) << (bits - 1)) - 1,
	}
}

func (c *Chunker) Next() ([]byte, error) {
	if c.err == io.EOF && len(c.buf) == 0 {
		return nil, io.EOF
	}
	if len(c.buf) < ChunkMax && c.err == nil {
		err := c.fill()
		if err == io.EOF {
			c.err = io.EOF
		} else if err != nil {
			return nil, err
		}
	}
	if len(c.buf) == 0 {
		if c.err != nil {
			return nil, c.err
		}
		return nil, io.EOF
	}
	n := c.cut(c.buf)
	if n <= 0 || n > len(c.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, n)
	copy(out, c.buf[:n])
	remain := len(c.buf) - n
	copy(c.buf, c.buf[n:n+remain])
	c.buf = c.buf[:remain]
	return out, nil
}

func (c *Chunker) fill() error {
	if cap(c.buf) < ChunkMax {
		nbuf := make([]byte, len(c.buf), ChunkMax)
		copy(nbuf, c.buf)
		c.buf = nbuf
	}
	for len(c.buf) < ChunkMax {
		n, err := c.r.Read(c.buf[len(c.buf):ChunkMax])
		c.buf = c.buf[:len(c.buf)+n]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}

func (c *Chunker) cut(data []byte) int {
	n := len(data)
	if n <= ChunkMin {
		return n
	}
	limit := n
	if limit > ChunkMax {
		limit = ChunkMax
	}
	var fp uint64
	i := ChunkMin
	for i < limit {
		fp = (fp << 1) + gear[data[i]]
		mask := c.maskL
		if i < ChunkAvg {
			mask = c.maskS
		}
		i++
		if fp&mask == 0 {
			return i
		}
	}
	return limit
}
