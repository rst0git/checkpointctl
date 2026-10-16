// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"archive/tar"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metadata "github.com/checkpoint-restore/checkpointctl/lib"
	"github.com/checkpoint-restore/go-criu/v8/crit"
	criu_core "github.com/checkpoint-restore/go-criu/v8/crit/images/criu-core"
	"github.com/checkpoint-restore/go-criu/v8/crit/images/mm"
	"github.com/checkpoint-restore/go-criu/v8/crit/images/pagemap"
	"github.com/checkpoint-restore/go-criu/v8/crit/images/pstree"
	"google.golang.org/protobuf/proto"
)

// Legacy images use the pagemap format from CRIU versions before 4.2.
// go-criu reads their page counts from compat_nr_pages because the newer
// nr_pages field is absent.
func TestMemparseLegacyPageCount(t *testing.T) {
	oldPID, oldOutput := *pID, *outputFilePath
	oldSearch, oldRegex, oldContext := *searchPattern, *searchRegexPattern, *searchContext
	t.Cleanup(func() {
		*pID = oldPID
		*outputFilePath = oldOutput
		*searchPattern = oldSearch
		*searchRegexPattern = oldRegex
		*searchContext = oldContext
	})

	archive := createLegacyMemoryArchive(t)
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "size",
			want: []string{"1 test-process " + metadata.ByteToString(int64(2*pageSize)) + " 0 B"},
		},
		{
			name: "pages",
			args: []string{"--pid=1"},
			want: []string{
				fmt.Sprintf("%016x", uint64(pageSize)),
				"|first-page-data!|",
				fmt.Sprintf("%016x", uint64(3*pageSize-chunkSize)),
				"|last-page-bytes!|",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := MemParse()
			cmd.SetArgs(append([]string{archive}, tc.args...))
			output := captureDiffOutput(t, func() {
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
			})
			output = strings.Join(strings.Fields(output), " ")
			for _, want := range tc.want {
				if !strings.Contains(output, want) {
					t.Errorf("memparse output does not contain %q:\n%s", want, output)
				}
			}
		})
	}
}

func createLegacyMemoryArchive(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	checkpoint := filepath.Join(dir, metadata.CheckpointDirectory)
	if err := os.Mkdir(checkpoint, 0o700); err != nil {
		t.Fatal(err)
	}

	writeImage := func(name, magic string, messages ...proto.Message) {
		t.Helper()
		file, err := os.Create(filepath.Join(checkpoint, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := file.Close(); err != nil {
				t.Error(err)
			}
		}()

		image := &crit.CriuImage{Magic: magic}
		for _, message := range messages {
			image.Entries = append(image.Entries, &crit.CriuEntry{Message: message})
		}
		if err := crit.New(nil, file, "", false, false).Encode(image); err != nil {
			t.Fatalf("encoding %s: %v", name, err)
		}
	}

	writeImage("pstree.img", "PSTREE", &pstree.PstreeEntry{
		Pid:     proto.Uint32(1),
		Ppid:    proto.Uint32(0),
		Pgid:    proto.Uint32(1),
		Sid:     proto.Uint32(1),
		Threads: []uint32{1},
	})
	writeImage("core-1.img", "CORE", &criu_core.CoreEntry{
		Mtype: criu_core.CoreEntry_X86_64.Enum(),
		Tc: &criu_core.TaskCoreEntry{
			TaskState:   proto.Uint32(uint32(crit.TaskAlive)),
			ExitCode:    proto.Uint32(0),
			Personality: proto.Uint32(0),
			Flags:       proto.Uint32(0),
			BlkSigset:   proto.Uint64(0),
			Comm:        proto.String("test-process"),
		},
	})
	writeImage("mm-1.img", "MM", &mm.MmEntry{
		MmStartCode:  proto.Uint64(0),
		MmEndCode:    proto.Uint64(0),
		MmStartData:  proto.Uint64(0),
		MmEndData:    proto.Uint64(0),
		MmStartStack: proto.Uint64(0),
		MmStartBrk:   proto.Uint64(0),
		MmBrk:        proto.Uint64(0),
		MmArgStart:   proto.Uint64(0),
		MmArgEnd:     proto.Uint64(0),
		MmEnvStart:   proto.Uint64(0),
		MmEnvEnd:     proto.Uint64(0),
		ExeFileId:    proto.Uint32(0),
	})
	writeImage("pagemap-1.img", "PAGEMAP",
		&pagemap.PagemapHead{PagesId: proto.Uint32(1)},
		&pagemap.PagemapEntry{Vaddr: proto.Uint64(uint64(pageSize)), CompatNrPages: proto.Uint32(2)},
	)

	pages := make([]byte, 2*pageSize)
	copy(pages, "first-page-data!")
	copy(pages[len(pages)-chunkSize:], "last-page-bytes!")
	if err := os.WriteFile(filepath.Join(checkpoint, "pages-1.img"), pages, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{metadata.ConfigDumpFile, metadata.SpecDumpFile} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	file, err := os.Create(filepath.Join(t.TempDir(), "checkpoint.tar"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()

	writer := tar.NewWriter(file)
	if err := writer.AddFS(os.DirFS(dir)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}
