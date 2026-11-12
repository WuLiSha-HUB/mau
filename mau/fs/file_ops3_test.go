package fs

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStatFactsFile 文件状态——大小 / 时间 / 非目录。
func TestStatFactsFile(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := svc.StatFacts("w:a.txt", false)
	if err != nil {
		t.Fatalf("状态读取失败: %v", err)
	}
	if facts.IsDirectory || facts.SizeBytes != 3 || facts.Recursive {
		t.Fatalf("事实 = %+v", facts)
	}
	if facts.LastWrite.IsZero() || facts.CreationTime.IsZero() {
		t.Fatalf("时间戳 = %+v", facts)
	}
}

// TestStatFactsDirectoryRecursive 目录状态——递归统计 / 非递归零计数。
func TestStatFactsDirectoryRecursive(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "deep", "b.txt"), []byte("bcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := svc.StatFacts("w:sub", true)
	if err != nil {
		t.Fatalf("状态读取失败: %v", err)
	}
	if !facts.IsDirectory || !facts.Recursive {
		t.Fatalf("类型/递归 = %+v", facts)
	}
	if facts.FileCount != 2 || facts.DirectoryCount != 1 || facts.TotalBytes != 4 {
		t.Fatalf("递归统计 = %+v", facts)
	}
	plain, err := svc.StatFacts("w:sub", false)
	if err != nil {
		t.Fatal(err)
	}
	if plain.Recursive || plain.FileCount != 0 || plain.TotalBytes != 0 {
		t.Fatalf("非递归 = %+v", plain)
	}
}

// TestStatFactsMissing 缺失路径。
func TestStatFactsMissing(t *testing.T) {
	svc, _, _ := newService(t)
	_, err := svc.StatFacts("w:nope", false)
	if err == nil || ErrText(err) != "ERR|FileNotFoundException|Path was not found." {
		t.Fatalf("缺失回执 = %v", err)
	}
}

// TestHashFacts SHA256 已知值。
func TestHashFacts(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	facts, err := svc.HashFacts("w:a.txt")
	if err != nil {
		t.Fatalf("哈希失败: %v", err)
	}
	if facts.Hash != "BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD" {
		t.Fatalf("摘要 = %s", facts.Hash)
	}
	if facts.Algorithm != "SHA256" || facts.SizeBytes != 3 {
		t.Fatalf("事实 = %+v", facts)
	}
	if facts.ElapsedMs < 0 {
		t.Fatalf("耗时 = %d", facts.ElapsedMs)
	}
}

// TestHashFactsErrors 目录 / 缺失。
func TestHashFactsErrors(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HashFacts("w:d"); err == nil || ErrText(err) != "ERR|InvalidDataException|Path is a directory (hash requires a file)." {
		t.Fatalf("目录回执 = %v", err)
	}
	if _, err := svc.HashFacts("w:nope.txt"); err == nil || ErrText(err) != "ERR|FileNotFoundException|File was not found." {
		t.Fatalf("缺失回执 = %v", err)
	}
}

// TestDiffFiles 文件模式——相同 / 不同（首个偏移 / 大小差）。
func TestDiffFiles(t *testing.T) {
	svc, root, _ := newService(t)
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "abc")
	write("b.txt", "abd")
	write("c.txt", "abcd")
	facts, err := svc.DiffFacts("w:a.txt", "w:a.txt", false, 200)
	if err != nil {
		t.Fatalf("对比失败: %v", err)
	}
	if !facts.BothFiles || !facts.Same || facts.FirstDiffOffset != -1 || facts.SameCount != 1 || facts.DiffTotal != 0 {
		t.Fatalf("相同事实 = %+v", facts)
	}
	facts, err = svc.DiffFacts("w:a.txt", "w:b.txt", false, 200)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Same || facts.FirstDiffOffset != 2 || facts.DiffTotal != 1 {
		t.Fatalf("不同事实 = %+v", facts)
	}
	facts, err = svc.DiffFacts("w:a.txt", "w:c.txt", false, 200)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Same || facts.FirstDiffOffset != 3 || facts.LeftSizeBytes != 3 || facts.RightSizeBytes != 4 {
		t.Fatalf("长短事实 = %+v", facts)
	}
}

// TestDiffDirectory 目录模式——only-left / only-right / 大小差 / 逐字节开关。
func TestDiffDirectory(t *testing.T) {
	svc, root, _ := newService(t)
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("left/a.txt", "A")
	write("left/b.txt", "B")
	write("left/sub/c.txt", "C")
	write("right/a.txt", "A")
	write("right/b.txt", "BB")
	write("right/sub/c.txt", "X")
	write("right/d.txt", "D")
	// 默认口径：路径集合 + 大小
	facts, err := svc.DiffFacts("w:left", "w:right", false, 200)
	if err != nil {
		t.Fatalf("对比失败: %v", err)
	}
	if facts.BothFiles || facts.Same || facts.DiffTotal != 2 || facts.SameCount != 2 || facts.Total != 4 || facts.ContentCompared {
		t.Fatalf("大小口径 = %+v", facts)
	}
	if len(facts.Entries) != 2 || facts.Entries[0].RelativePath != "b.txt" || facts.Entries[0].Status != "changed" ||
		facts.Entries[1].RelativePath != "d.txt" || facts.Entries[1].Status != "only-right" {
		t.Fatalf("差异清单 = %+v", facts.Entries)
	}
	// 逐字节口径：同大小但内容不同的 sub/c.txt 也进清单
	facts, err = svc.DiffFacts("w:left", "w:right", true, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.ContentCompared || facts.DiffTotal != 3 || facts.SameCount != 1 {
		t.Fatalf("逐字节口径 = %+v", facts)
	}
	want := []DiffEntry{
		{RelativePath: "b.txt", Status: "changed"},
		{RelativePath: "d.txt", Status: "only-right"},
		{RelativePath: "sub/c.txt", Status: "changed"},
	}
	if len(facts.Entries) != len(want) {
		t.Fatalf("清单条数 = %d", len(facts.Entries))
	}
	for i := range want {
		if facts.Entries[i] != want[i] {
			t.Fatalf("清单[%d] = %+v，期望 %+v", i, facts.Entries[i], want[i])
		}
	}
}

// TestDiffDirectoryIgnore 忽略目录（.git/bin/obj 等）不参与比对。
func TestDiffDirectoryIgnore(t *testing.T) {
	svc, root, _ := newService(t)
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("left/a.txt", "A")
	write("left/bin/skip.txt", "S")
	write("right/a.txt", "A")
	write("right/bin/skip.txt", "T")
	facts, err := svc.DiffFacts("w:left", "w:right", false, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Same || facts.IgnoredDirs != 2 || facts.Total != 1 {
		t.Fatalf("忽略统计 = %+v", facts)
	}
}

// TestDiffLimit 截断与 limit 边界。
func TestDiffLimit(t *testing.T) {
	svc, root, _ := newService(t)
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("left/a.txt", "a")
	write("left/b.txt", "b")
	write("left/c.txt", "c")
	if err := os.MkdirAll(filepath.Join(root, "right"), 0o755); err != nil {
		t.Fatal(err)
	}
	facts, err := svc.DiffFacts("w:left", "w:right", false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if facts.DiffTotal != 3 || len(facts.Entries) != 2 {
		t.Fatalf("截断 = %+v", facts)
	}
	facts, err = svc.DiffFacts("w:left", "w:right", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if facts.DiffTotal != 3 || len(facts.Entries) != 0 {
		t.Fatalf("limit=0 = %+v", facts)
	}
	if _, err := svc.DiffFacts("w:left", "w:right", false, -1); err == nil || ErrText(err) != "ERR|OverflowException|Arithmetic operation resulted in an overflow." {
		t.Fatalf("limit=-1 回执 = %v", err)
	}
}

// TestDiffErrors 缺失 / 类型不一致。
func TestDiffErrors(t *testing.T) {
	svc, root, _ := newService(t)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DiffFacts("w:nope", "w:a.txt", false, 200); err == nil || ErrText(err) != "ERR|FileNotFoundException|Left path was not found." {
		t.Fatalf("左缺失 = %v", err)
	}
	if _, err := svc.DiffFacts("w:a.txt", "w:nope", false, 200); err == nil || ErrText(err) != "ERR|FileNotFoundException|Right path was not found." {
		t.Fatalf("右缺失 = %v", err)
	}
	if _, err := svc.DiffFacts("w:a.txt", "w:d", false, 200); err == nil || ErrText(err) != "ERR|InvalidDataException|Diff requires the same kind on both sides (left=file, right=directory)." {
		t.Fatalf("类型不一致 = %v", err)
	}
}
