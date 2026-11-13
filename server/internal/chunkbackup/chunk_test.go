package chunkbackup

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMilestoneSnapshotOneByte(t *testing.T) {
	root := t.TempDir()
	world := filepath.Join(root, "world")
	if err := os.MkdirAll(world, 0o755); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, ChunkMax+4096)
	for i := range big {
		big[i] = byte(i*31 + i*i)
	}
	keep := []byte("keep-me")
	if err := os.WriteFile(filepath.Join(world, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(world, "keep.txt"), keep, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(filepath.Join(root, "chunks"))
	if err != nil {
		t.Fatal(err)
	}
	files1, fresh1, err := Ingest(st, world, "world", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fresh1 < int64(len(big)) {
		t.Fatalf("milestone fresh %d file %d", fresh1, len(big))
	}
	man1 := Manifest{
		Version: 1, ID: "milestone-1", Kind: "milestone", Scope: "world",
		Roots: []string{"world"}, Files: files1,
	}
	sum1, err := ContentHash(man1)
	if err != nil || len(sum1) != 64 {
		t.Fatal(sum1, err)
	}
	big[len(big)-1] ^= 0x5a
	if err := os.WriteFile(filepath.Join(world, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	files2, fresh2, err := Ingest(st, world, "world", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fresh2 <= 0 || fresh2 >= int64(len(big)) {
		t.Fatalf("snapshot fresh %d file %d", fresh2, len(big))
	}
	disk, err := st.DiskSize(files2, HashSet(files1))
	if err != nil {
		t.Fatal(err)
	}
	if disk <= 0 || disk >= int64(len(big)) {
		t.Fatalf("disk %d file %d", disk, len(big))
	}
	man2 := Manifest{
		Version: 1, ID: "snapshot-1", Kind: "snapshot", Scope: "world", Parent: man1.ID,
		Roots: []string{"world"}, Files: files2, Deleted: DeletedPaths(files1, files2),
	}
	sum2, err := ContentHash(man2)
	if err != nil || sum2 == sum1 || len(sum2) != 64 {
		t.Fatal(sum1, sum2, err)
	}
	if err := SaveManifest(ManifestPath(root, man1.ID), man1); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(ManifestPath(root, man2.ID), man2); err != nil {
		t.Fatal(err)
	}
	mPath := filepath.Join(root, "m.tar.zst")
	sPath := filepath.Join(root, "s.tar.zst")
	if err := writeFileArchive(mPath, st, man1, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeFileArchive(sPath, st, man2, &man1); err != nil {
		t.Fatal(err)
	}
	origBig := append([]byte(nil), big...)
	origBig[len(origBig)-1] ^= 0x5a
	mileOut := filepath.Join(root, "mile")
	if err := Merge(mPath, "", mileOut); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(mileOut, "world", "big.bin")), origBig) {
		t.Fatal("milestone big")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(mileOut, "world", "keep.txt")), keep) {
		t.Fatal("milestone keep")
	}
	snapOut := filepath.Join(root, "snap")
	if err := Merge(mPath, sPath, snapOut); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustRead(t, filepath.Join(snapOut, "world", "big.bin")), big) {
		t.Fatal("snapshot big")
	}
	if !bytes.Equal(mustRead(t, filepath.Join(snapOut, "world", "keep.txt")), keep) {
		t.Fatal("snapshot keep")
	}
}

func writeFileArchive(path string, st *Store, man Manifest, parent *Manifest) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = WriteArchive(f, st, man, parent)
	cerr := f.Close()
	if err != nil {
		return err
	}
	return cerr
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
