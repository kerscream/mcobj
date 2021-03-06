package mcworld

import (
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ChunkNotFoundError = errors.New("Chunk Missing")
)

type BetaWorld struct {
	worldDir string
}

type McrFile struct {
	*os.File
}

func (w *BetaWorld) OpenChunk(x, z int) (io.ReadCloser, error) {
	mcaName := fmt.Sprintf("r.%v.%v.mca", x>>5, z>>5)
	mcaPath := filepath.Join(w.worldDir, "region", mcaName)

	mcrName := fmt.Sprintf("r.%v.%v.mcr", x>>5, z>>5)
	mcrPath := filepath.Join(w.worldDir, "region", mcrName)

	var path string
	if _, err := os.Stat(mcaPath); err == nil {
		path = mcaPath
	} else {
		path = mcrPath
	}

	file, openErr := os.Open(path)
	if openErr != nil {
		return nil, openErr
	}
	defer func() {
		if file != nil {
			file.Close()
		}
	}()

	var mcr = &McrFile{file}
	var loc, readLocErr = mcr.ReadLocation(x, z)
	if readLocErr != nil {
		return nil, readLocErr
	}

	if loc == 0 {
		return nil, errors.New(fmt.Sprintf("Chunk missing: %v,%v in %v. %v", x, z, mcaName, (x&31)+(z&31)*32))
	}

	var (
		length          uint32
		compressionType byte
	)

	var _, seekErr = mcr.Seek(int64(loc.Offset()), 0)
	if seekErr != nil {
		return nil, seekErr
	}

	var lengthReadErr = binary.Read(mcr, binary.BigEndian, &length)
	if lengthReadErr != nil {
		return nil, lengthReadErr
	}

	var compressionTypeErr = binary.Read(mcr, binary.BigEndian, &compressionType)
	if compressionTypeErr != nil {
		return nil, compressionTypeErr
	}

	var r, zlibNewErr = zlib.NewReader(mcr)
	if zlibNewErr != nil {
		return nil, zlibNewErr
	}

	var pair = &ReadCloserPair{r, file}
	file = nil
	return pair, nil
}

func (r McrFile) ReadLocation(x, z int) (ChunkLocation, error) {
	var _, seekErr = r.Seek(int64(4*((x&31)+(z&31)*32)), 0)
	if seekErr != nil {
		return ChunkLocation(0), seekErr
	}
	var location uint32
	var readErr = binary.Read(r, binary.BigEndian, &location)
	if readErr != nil {
		return ChunkLocation(0), readErr
	}
	return ChunkLocation(location), nil
}

type ChunkLocation uint32

func (cl ChunkLocation) Offset() int {
	return 4096 * (int(cl) >> 8)
}

func (cl ChunkLocation) Sectors() int {
	return (int(cl) & 0xff)
}

func (w *BetaWorld) ChunkPool(mask ChunkMask) (ChunkPool, error) {
	var regionDirname = filepath.Join(w.worldDir, "region")
	var dir, dirOpenErr = os.Open(regionDirname)
	if dirOpenErr != nil {
		return nil, dirOpenErr
	}
	defer dir.Close()

	var pool = &BetaChunkPool{make(map[uint64]bool), EmptyBoundingBox()}

	for {
		var filenames, readErr = dir.Readdirnames(1)
		if readErr == io.EOF || len(filenames) == 0 {
			break
		}
		if readErr != nil {
			return nil, readErr
		}

		var fields = strings.FieldsFunc(filenames[0], func(c rune) bool { return c == '.' })

		if len(fields) == 4 {
			var (
				rx, rxErr = strconv.Atoi(fields[1])
				rz, ryErr = strconv.Atoi(fields[2])
			)

			if rxErr == nil && ryErr == nil {
				var regionFilename = filepath.Join(regionDirname, filenames[0])
				var mcrErr = w.poolMcrChunks(regionFilename, mask, pool, rx, rz)
				if mcrErr != nil {
					return nil, mcrErr
				}
			}
		}
	}

	return pool, nil
}

func (w *BetaWorld) poolMcrChunks(regionFilename string, mask ChunkMask, pool *BetaChunkPool, rx, rz int) error {
	var region, regionOpenErr = os.Open(regionFilename)
	if regionOpenErr != nil {
		return regionOpenErr
	}
	defer region.Close()

	for cz := 0; cz < 32; cz++ {
		for cx := 0; cx < 32; cx++ {
			var location uint32
			var readErr = binary.Read(region, binary.BigEndian, &location)
			if readErr == io.EOF {
				continue
			}
			if readErr != nil {
				return readErr
			}
			if location != 0 {
				var (
					x = rx*32 + cx
					z = rz*32 + cz
				)

				if !mask.IsMasked(x, z) {
					pool.chunkMap[betaChunkPoolKey(x, z)] = true
					pool.box.Union(x, z)
				}
			}
		}
	}

	return nil
}

type BetaChunkPool struct {
	chunkMap map[uint64]bool
	box      *BoundingBox
}

func (p *BetaChunkPool) Pop(x, z int) bool {
	var key = betaChunkPoolKey(x, z)
	var _, exists = p.chunkMap[key]
	delete(p.chunkMap, key)
	return exists
}

func (p *BetaChunkPool) Remaining() int {
	return len(p.chunkMap)
}

func (p *BetaChunkPool) BoundingBox() *BoundingBox {
	return p.box
}

func betaChunkPoolKey(x, z int) uint64 {
	return uint64(x)<<32 + uint64(z)
}
