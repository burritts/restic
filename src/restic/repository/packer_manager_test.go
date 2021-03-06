package repository

import (
	"io"
	"math/rand"
	"os"
	"restic"
	"restic/backend/mem"
	"restic/crypto"
	"restic/mock"
	"testing"
)

type randReader struct {
	src  rand.Source
	rand *rand.Rand
}

func newRandReader(src rand.Source) *randReader {
	return &randReader{
		src:  src,
		rand: rand.New(src),
	}
}

// Read generates len(p) random bytes and writes them into p. It
// always returns len(p) and a nil error.
func (r *randReader) Read(p []byte) (n int, err error) {
	for i := 0; i < len(p); i += 7 {
		val := r.src.Int63()
		for j := 0; i+j < len(p) && j < 7; j++ {
			p[i+j] = byte(val)
			val >>= 8
		}
	}
	return len(p), nil
}

func randomID(rd io.Reader) restic.ID {
	id := restic.ID{}
	_, err := io.ReadFull(rd, id[:])
	if err != nil {
		panic(err)
	}
	return id
}

const maxBlobSize = 1 << 20

func saveFile(t testing.TB, be Saver, f *os.File, id restic.ID) {
	h := restic.Handle{Type: restic.DataFile, Name: id.String()}
	t.Logf("save file %v", h)

	if err := be.Save(h, f); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(f.Name()); err != nil {
		t.Fatal(err)
	}
}

func fillPacks(t testing.TB, rnd *randReader, be Saver, pm *packerManager, buf []byte) (bytes int) {
	for i := 0; i < 100; i++ {
		l := rnd.rand.Intn(1 << 20)
		seed := rnd.rand.Int63()

		packer, err := pm.findPacker(uint(l))
		if err != nil {
			t.Fatal(err)
		}

		rd := newRandReader(rand.NewSource(seed))
		id := randomID(rd)
		buf = buf[:l]
		_, err = io.ReadFull(rd, buf)
		if err != nil {
			t.Fatal(err)
		}

		n, err := packer.Add(restic.DataBlob, id, buf)
		if n != l {
			t.Errorf("Add() returned invalid number of bytes: want %v, got %v", n, l)
		}
		bytes += l

		if packer.Size() < minPackSize && pm.countPacker() < maxPackers {
			pm.insertPacker(packer)
			continue
		}

		_, err = packer.Finalize()
		if err != nil {
			t.Fatal(err)
		}

		if _, err = packer.tmpfile.Seek(0, 0); err != nil {
			t.Fatal(err)
		}

		packID := restic.IDFromHash(packer.hw.Sum(nil))
		saveFile(t, be, packer.tmpfile, packID)
	}

	return bytes
}

func flushRemainingPacks(t testing.TB, rnd *randReader, be Saver, pm *packerManager) (bytes int) {
	if pm.countPacker() > 0 {
		for _, packer := range pm.packers {
			n, err := packer.Finalize()
			if err != nil {
				t.Fatal(err)
			}
			bytes += int(n)

			packID := restic.IDFromHash(packer.hw.Sum(nil))
			saveFile(t, be, packer.tmpfile, packID)
		}
	}

	return bytes
}

func TestPackerManager(t *testing.T) {
	rnd := newRandReader(rand.NewSource(23))

	be := mem.New()
	pm := newPackerManager(be, crypto.NewRandomKey())

	blobBuf := make([]byte, maxBlobSize)

	bytes := fillPacks(t, rnd, be, pm, blobBuf)
	bytes += flushRemainingPacks(t, rnd, be, pm)

	t.Logf("saved %d bytes", bytes)
}

func BenchmarkPackerManager(t *testing.B) {
	rnd := newRandReader(rand.NewSource(23))

	be := &mock.Backend{
		SaveFn: func(restic.Handle, io.Reader) error { return nil },
	}
	blobBuf := make([]byte, maxBlobSize)

	t.ResetTimer()

	for i := 0; i < t.N; i++ {
		bytes := 0
		pm := newPackerManager(be, crypto.NewRandomKey())
		bytes += fillPacks(t, rnd, be, pm, blobBuf)
		bytes += flushRemainingPacks(t, rnd, be, pm)
		t.Logf("saved %d bytes", bytes)
	}
}
