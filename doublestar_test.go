package doublestar

import (
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type MatchTest struct {
	pattern, testPath string // a pattern and path to test the pattern on
	shouldMatch       bool   // true if the pattern should match the path
	expectedErr       error  // an expected error
	expectIOErr       bool   // whether or not to expect an io error
	isStandard        bool   // pattern doesn't use any doublestar features (e.g. '**', '{a,b}')
	testOnDisk        bool   // true: test pattern against files in "test" directory
	numResults        int    // number of glob results if testing on disk
	winNumResults     int    // number of glob results on Windows
}

// Tests which contain escapes and symlinks will not work on Windows
var onWindows = runtime.GOOS == "windows"

var matchTests = []MatchTest{
	{"*", "", true, nil, false, true, false, 0, 0},
	{"*", "/", false, nil, false, true, false, 0, 0},
	{"/*", "/", true, nil, false, true, false, 0, 0},
	{"/*", "/debug/", false, nil, false, true, false, 0, 0},
	{"/*", "//", false, nil, false, true, false, 0, 0},
	{"abc", "abc", true, nil, false, true, true, 1, 1},
	{"*", "abc", true, nil, false, true, true, 20, 16},
	{"*c", "abc", true, nil, false, true, true, 2, 2},
	{"*/", "a/", true, nil, false, true, false, 0, 0},
	{"a*", "a", true, nil, false, true, true, 9, 9},
	{"a*", "abc", true, nil, false, true, true, 9, 9},
	{"a*", "ab/c", false, nil, false, true, true, 9, 9},
	{"a*/b", "abc/b", true, nil, !onWindows, true, true, 2, 2},
	{"a*/b", "a/c/b", false, nil, !onWindows, true, true, 2, 2},
	{"a*b*c*d*e*", "axbxcxdxe", true, nil, false, true, true, 3, 3},
	{"a*b*c*d*e*/f", "axbxcxdxe/f", true, nil, !onWindows, true, true, 2, 2},
	{"a*b*c*d*e*/f", "axbxcxdxexxx/f", true, nil, !onWindows, true, true, 2, 2},
	{"a*b*c*d*e*/f", "axbxcxdxe/xxx/f", false, nil, !onWindows, true, true, 2, 2},
	{"a*b*c*d*e*/f", "axbxcxdxexxx/fff", false, nil, !onWindows, true, true, 2, 2},
	{"a*b?c*x", "abxbbxdbxebxczzx", true, nil, false, true, true, 2, 2},
	{"a*b?c*x", "abxbbxdbxebxczzy", false, nil, false, true, true, 2, 2},
	{"ab[c]", "abc", true, nil, false, true, true, 1, 1},
	{"ab[b-d]", "abc", true, nil, false, true, true, 1, 1},
	{"ab[e-g]", "abc", false, nil, false, true, true, 0, 0},
	{"ab[^c]", "abc", false, nil, false, true, true, 0, 0},
	{"ab[^b-d]", "abc", false, nil, false, true, true, 0, 0},
	{"ab[^e-g]", "abc", true, nil, false, true, true, 1, 1},
	{"a\\*b", "ab", false, nil, true, true, !onWindows, 0, 0},
	{"a?b", "a☺b", true, nil, false, true, true, 1, 1},
	{"a[^a]b", "a☺b", true, nil, false, true, true, 1, 1},
	{"a[!a]b", "a☺b", true, nil, false, false, true, 1, 1},
	{"a???b", "a☺b", false, nil, false, true, true, 0, 0},
	{"a[^a][^a][^a]b", "a☺b", false, nil, false, true, true, 0, 0},
	{"[a-ζ]*", "α", true, nil, false, true, true, 18, 16},
	{"*[a-ζ]", "A", false, nil, false, true, true, 18, 16},
	{"a?b", "a/b", false, nil, false, true, true, 1, 1},
	{"a*b", "a/b", false, nil, false, true, true, 1, 1},
	{"[\\]a]", "]", true, nil, false, true, !onWindows, 2, 2},
	{"[\\-]", "-", true, nil, false, true, !onWindows, 1, 1},
	{"[x\\-]", "x", true, nil, false, true, !onWindows, 2, 2},
	{"[x\\-]", "-", true, nil, false, true, !onWindows, 2, 2},
	{"[x\\-]", "z", false, nil, false, true, !onWindows, 2, 2},
	{"[\\-x]", "x", true, nil, false, true, !onWindows, 2, 2},
	{"[\\-x]", "-", true, nil, false, true, !onWindows, 2, 2},
	{"[\\-x]", "a", false, nil, false, true, !onWindows, 2, 2},
	{"[]a]", "]", false, ErrBadPattern, false, true, true, 0, 0},
	// doublestar, like bash, allows these when path.Match() does not
	{"[-]", "-", true, nil, false, false, !onWindows, 1, 0},
	{"[x-]", "x", true, nil, false, false, true, 2, 1},
	{"[x-]", "-", true, nil, false, false, !onWindows, 2, 1},
	{"[x-]", "z", false, nil, false, false, true, 2, 1},
	{"[-x]", "x", true, nil, false, false, true, 2, 1},
	{"[-x]", "-", true, nil, false, false, !onWindows, 2, 1},
	{"[-x]", "a", false, nil, false, false, true, 2, 1},
	{"[a-b-d]", "a", true, nil, false, false, true, 3, 2},
	{"[a-b-d]", "b", true, nil, false, false, true, 3, 2},
	{"[a-b-d]", "-", true, nil, false, false, !onWindows, 3, 2},
	{"[a-b-d]", "c", false, nil, false, false, true, 3, 2},
	{"[a-b-x]", "x", true, nil, false, false, true, 4, 3},
	{"\\", "a", false, ErrBadPattern, false, true, !onWindows, 0, 0},
	{"[", "a", false, ErrBadPattern, false, true, true, 0, 0},
	{"[^", "a", false, ErrBadPattern, false, true, true, 0, 0},
	{"[^bc", "a", false, ErrBadPattern, false, true, true, 0, 0},
	{"a[", "a", false, ErrBadPattern, false, true, true, 0, 0},
	{"a[", "ab", false, ErrBadPattern, false, true, true, 0, 0},
	{"ad[", "ab", false, ErrBadPattern, false, true, true, 0, 0},
	{"*x", "xxx", true, nil, false, true, true, 4, 4},
	{"[abc]", "b", true, nil, false, true, true, 3, 3},
	{"**", "", true, nil, false, false, false, 38, 38},
	{"a/**", "a", true, nil, false, false, true, 7, 7},
	{"a/**", "a/", true, nil, false, false, false, 7, 7},
	{"a/**", "a/b", true, nil, false, false, true, 7, 7},
	{"a/**", "a/b/c", true, nil, false, false, true, 7, 7},
	{"**/c", "c", true, nil, !onWindows, false, true, 5, 4},
	{"**/c", "b/c", true, nil, !onWindows, false, true, 5, 4},
	{"**/c", "a/b/c", true, nil, !onWindows, false, true, 5, 4},
	{"**/c", "a/b", false, nil, !onWindows, false, true, 5, 4},
	{"**/c", "abcd", false, nil, !onWindows, false, true, 5, 4},
	{"**/c", "a/abc", false, nil, !onWindows, false, true, 5, 4},
	{"a/**/b", "a/b", true, nil, false, false, true, 2, 2},
	{"a/**/c", "a/b/c", true, nil, false, false, true, 2, 2},
	{"a/**/d", "a/b/c/d", true, nil, false, false, true, 1, 1},
	{"a/\\**", "a/b/c", false, nil, false, false, !onWindows, 0, 0},
	{"a/\\[*\\]", "a/bc", false, nil, false, true, !onWindows, 0, 0},
	// this fails the FilepathGlob test on Windows
	{"a/b/c", "a/b//c", false, nil, false, true, !onWindows, 1, 1},
	// odd: Glob + filepath.Glob return results
	{"a/", "a", false, nil, false, true, false, 0, 0},
	{"ab{c,d}", "abc", true, nil, true, false, true, 1, 1},
	{"ab{c,d,*}", "abcde", true, nil, true, false, true, 5, 5},
	{"ab{c,d}[", "abcd", false, ErrBadPattern, false, false, true, 0, 0},
	{"a{,bc}", "a", true, nil, false, false, true, 2, 2},
	{"a{,bc}", "abc", true, nil, false, false, true, 2, 2},
	{"a/{b/c,c/b}", "a/b/c", true, nil, false, false, true, 2, 2},
	{"a/{b/c,c/b}", "a/c/b", true, nil, false, false, true, 2, 2},
	{"a/a*{b,c}", "a/abc", true, nil, false, false, true, 1, 1},
	{"{a/{b,c},abc}", "a/b", true, nil, false, false, true, 3, 3},
	{"{a/{b,c},abc}", "a/c", true, nil, false, false, true, 3, 3},
	{"{a/{b,c},abc}", "abc", true, nil, false, false, true, 3, 3},
	{"{a/{b,c},abc}", "a/b/c", false, nil, false, false, true, 3, 3},
	{"{a/ab*}", "a/abc", true, nil, false, false, true, 1, 1},
	{"{a/*}", "a/b", true, nil, false, false, true, 3, 3},
	{"{a/abc}", "a/abc", true, nil, false, false, true, 1, 1},
	{"{a/b,a/c}", "a/c", true, nil, false, false, true, 2, 2},
	{"abc/**", "abc/b", true, nil, false, false, true, 3, 3},
	{"**/abc", "abc", true, nil, !onWindows, false, true, 2, 2},
	{"abc**", "abc/b", false, nil, false, false, true, 3, 3},
	{"**/*.txt", "abc/【test】.txt", true, nil, !onWindows, false, true, 1, 1},
	{"**/【*", "abc/【test】.txt", true, nil, !onWindows, false, true, 1, 1},
	{"**/{a,b}", "a/b", true, nil, true, false, true, 5, 5},
	// unfortunately, io/fs can't handle this, so neither can Glob =(
	{"broken-symlink", "broken-symlink", true, nil, false, true, false, 1, 1},
	{"working-symlink/c/*", "working-symlink/c/d", true, nil, false, true, !onWindows, 1, 1},
	{"working-sym*/*", "working-symlink/c", true, nil, true, true, !onWindows, 1, 1},
	{"b/**/f", "b/symlink-dir/f", true, nil, false, false, !onWindows, 2, 2},
	{"e/**", "e/**", true, nil, false, false, !onWindows, 11, 6},
	{"e/**", "e/*", true, nil, false, false, !onWindows, 11, 6},
	{"e/**", "e/?", true, nil, false, false, !onWindows, 11, 6},
	{"e/**", "e/[", true, nil, false, false, true, 11, 6},
	{"e/**", "e/]", true, nil, false, false, true, 11, 6},
	{"e/**", "e/[]", true, nil, false, false, true, 11, 6},
	{"e/**", "e/{", true, nil, false, false, true, 11, 6},
	{"e/**", "e/}", true, nil, false, false, true, 11, 6},
	{"e/**", "e/\\", true, nil, false, false, !onWindows, 11, 6},
	{"e/*", "e/*", true, nil, false, true, !onWindows, 10, 5},
	{"e/?", "e/?", true, nil, false, true, !onWindows, 7, 4},
	{"e/?", "e/*", true, nil, false, true, !onWindows, 7, 4},
	{"e/?", "e/[", true, nil, false, true, true, 7, 4},
	{"e/?", "e/]", true, nil, false, true, true, 7, 4},
	{"e/?", "e/{", true, nil, false, true, true, 7, 4},
	{"e/?", "e/}", true, nil, false, true, true, 7, 4},
	{"e/\\[", "e/[", true, nil, false, true, !onWindows, 1, 1},
	{"e/[", "e/[", false, ErrBadPattern, false, true, true, 0, 0},
	{"e/]", "e/]", true, nil, false, true, true, 1, 1},
	{"e/\\]", "e/]", true, nil, false, true, !onWindows, 1, 1},
	{"e/\\{", "e/{", true, nil, false, true, !onWindows, 1, 1},
	{"e/\\}", "e/}", true, nil, false, true, !onWindows, 1, 1},
	{"e/[\\*\\?]", "e/*", true, nil, false, true, !onWindows, 2, 2},
	{"e/[\\*\\?]", "e/?", true, nil, false, true, !onWindows, 2, 2},
	{"e/[\\*\\?]", "e/**", false, nil, false, true, !onWindows, 2, 2},
	{"e/[\\*\\?]?", "e/**", true, nil, false, true, !onWindows, 1, 1},
	{"e/{\\*,\\?}", "e/*", true, nil, false, false, !onWindows, 2, 2},
	{"e/{\\*,\\?}", "e/?", true, nil, false, false, !onWindows, 2, 2},
	{"e/\\*", "e/*", true, nil, false, true, !onWindows, 1, 1},
	{"e/\\?", "e/?", true, nil, false, true, !onWindows, 1, 1},
	{"e/\\?", "e/**", false, nil, false, true, !onWindows, 1, 1},
	{"nonexistent-path", "a", false, nil, true, true, true, 0, 0},
	{"nonexistent-path/file", "a", false, nil, true, true, true, 0, 0},
	{"nonexistent-path/*", "a", false, nil, true, true, true, 0, 0},
	{"nonexistent-path/**", "a", false, nil, true, true, true, 0, 0},
}

func TestValidatePattern(t *testing.T) {
	for idx, tt := range matchTests {
		testValidatePatternWith(t, idx, tt)
	}
}

func testValidatePatternWith(t *testing.T, idx int, tt MatchTest) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("#%v. Validate(%#q) panicked: %#v", idx, tt.pattern, r)
		}
	}()

	result := ValidatePattern(tt.pattern)
	if result != (tt.expectedErr == nil) {
		t.Errorf("#%v. ValidatePattern(%#q) = %v want %v", idx, tt.pattern, result, !result)
	}
}

func TestMatch(t *testing.T) {
	for idx, tt := range matchTests {
		// Since Match() always uses "/" as the separator, we
		// don't need to worry about the tt.testOnDisk flag
		testMatchWith(t, idx, tt)
	}
}

func testMatchWith(t *testing.T, idx int, tt MatchTest) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("#%v. Match(%#q, %#q) panicked: %#v", idx, tt.pattern, tt.testPath, r)
		}
	}()

	// Match() always uses "/" as the separator
	ok, err := Match(tt.pattern, tt.testPath)
	if ok != tt.shouldMatch || err != tt.expectedErr {
		t.Errorf("#%v. Match(%#q, %#q) = %v, %v want %v, %v", idx, tt.pattern, tt.testPath, ok, err, tt.shouldMatch, tt.expectedErr)
	}

	if tt.isStandard {
		stdOk, stdErr := path.Match(tt.pattern, tt.testPath)
		if ok != stdOk || !compareErrors(err, stdErr) {
			t.Errorf("#%v. Match(%#q, %#q) != path.Match(...). Got %v, %v want %v, %v", idx, tt.pattern, tt.testPath, ok, err, stdOk, stdErr)
		}
	}
}

func BenchmarkMatch(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range matchTests {
			if tt.isStandard {
				Match(tt.pattern, tt.testPath)
			}
		}
	}
}

func BenchmarkGoMatch(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range matchTests {
			if tt.isStandard {
				path.Match(tt.pattern, tt.testPath)
			}
		}
	}
}

func TestPathMatch(t *testing.T) {
	for idx, tt := range matchTests {
		// Even though we aren't actually matching paths on disk, we are using
		// PathMatch() which will use the system's separator. As a result, any
		// patterns that might cause problems on-disk need to also be avoided
		// here in this test.
		if tt.testOnDisk {
			testPathMatchWith(t, idx, tt)
		}
	}
}

func testPathMatchWith(t *testing.T, idx int, tt MatchTest) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("#%v. Match(%#q, %#q) panicked: %#v", idx, tt.pattern, tt.testPath, r)
		}
	}()

	pattern := filepath.FromSlash(tt.pattern)
	testPath := filepath.FromSlash(tt.testPath)
	ok, err := PathMatch(pattern, testPath)
	if ok != tt.shouldMatch || err != tt.expectedErr {
		t.Errorf("#%v. PathMatch(%#q, %#q) = %v, %v want %v, %v", idx, pattern, testPath, ok, err, tt.shouldMatch, tt.expectedErr)
	}

	if tt.isStandard {
		stdOk, stdErr := filepath.Match(pattern, testPath)
		if ok != stdOk || !compareErrors(err, stdErr) {
			t.Errorf("#%v. PathMatch(%#q, %#q) != filepath.Match(...). Got %v, %v want %v, %v", idx, pattern, testPath, ok, err, stdOk, stdErr)
		}
	}
}

func TestPathMatchFake(t *testing.T) {
	// This test fakes that our path separator is `\\` so we can test what it
	// would be like on Windows - obviously, we don't need to do that if we
	// actually _are_ on Windows, since TestPathMatch will cover it.
	if onWindows {
		return
	}

	for idx, tt := range matchTests {
		// Even though we aren't actually matching paths on disk, we are using
		// PathMatch() which will use the system's separator. As a result, any
		// patterns that might cause problems on-disk need to also be avoided
		// here in this test.
		// On Windows, escaping is disabled. Instead, '\\' is treated as path separator.
		// So it's not possible to match escaped wild characters.
		if tt.testOnDisk && !strings.Contains(tt.pattern, "\\") {
			testPathMatchFakeWith(t, idx, tt)
		}
	}
}

func testPathMatchFakeWith(t *testing.T, idx int, tt MatchTest) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("#%v. Match(%#q, %#q) panicked: %#v", idx, tt.pattern, tt.testPath, r)
		}
	}()

	pattern := strings.ReplaceAll(tt.pattern, "/", "\\")
	testPath := strings.ReplaceAll(tt.testPath, "/", "\\")
	ok, err := matchWithSeparator(pattern, testPath, '\\', true)
	if ok != tt.shouldMatch || err != tt.expectedErr {
		t.Errorf("#%v. PathMatch(%#q, %#q) = %v, %v want %v, %v", idx, pattern, testPath, ok, err, tt.shouldMatch, tt.expectedErr)
	}
}

func BenchmarkPathMatch(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range matchTests {
			if tt.isStandard && tt.testOnDisk {
				pattern := filepath.FromSlash(tt.pattern)
				testPath := filepath.FromSlash(tt.testPath)
				PathMatch(pattern, testPath)
			}
		}
	}
}

func BenchmarkGoPathMatch(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range matchTests {
			if tt.isStandard && tt.testOnDisk {
				pattern := filepath.FromSlash(tt.pattern)
				testPath := filepath.FromSlash(tt.testPath)
				filepath.Match(pattern, testPath)
			}
		}
	}
}

func TestGlob(t *testing.T) {
	doGlobTest(t)
}

func TestGlobWithFailOnIOErrors(t *testing.T) {
	doGlobTest(t, WithFailOnIOErrors())
}

func doGlobTest(t *testing.T, opts ...GlobOption) {
	glob := newGlob(opts...)
	fsys := os.DirFS("test")
	for idx, tt := range matchTests {
		if tt.testOnDisk {
			testGlobWith(t, idx, tt, glob, opts, fsys)
		}
	}
}

func testGlobWith(t *testing.T, idx int, tt MatchTest, g *glob, opts []GlobOption, fsys fs.FS) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("#%v. Glob(%#q, %#v) panicked: %#v", idx, tt.pattern, g, r)
		}
	}()

	matches, err := Glob(fsys, tt.pattern, opts...)
	verifyGlobResults(t, idx, "Glob", tt, g, fsys, matches, err)
	if len(opts) == 0 {
		testStandardGlob(t, idx, "Glob", tt, fsys, matches, err)
	}
}

func TestGlobWalk(t *testing.T) {
	doGlobWalkTest(t)
}

func TestGlobWalkWithFailOnIOErrors(t *testing.T) {
	doGlobWalkTest(t, WithFailOnIOErrors())
}

func doGlobWalkTest(t *testing.T, opts ...GlobOption) {
	glob := newGlob(opts...)
	fsys := os.DirFS("test")
	for idx, tt := range matchTests {
		if tt.testOnDisk {
			testGlobWalkWith(t, idx, tt, glob, opts, fsys)
		}
	}
}

func testGlobWalkWith(t *testing.T, idx int, tt MatchTest, g *glob, opts []GlobOption, fsys fs.FS) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("#%v. Glob(%#q, %#v) panicked: %#v", idx, tt.pattern, opts, r)
		}
	}()

	var matches []string
	err := GlobWalk(fsys, tt.pattern, func(p string, d fs.DirEntry) error {
		matches = append(matches, p)
		return nil
	}, opts...)
	verifyGlobResults(t, idx, "GlobWalk", tt, g, fsys, matches, err)
	if len(opts) == 0 {
		testStandardGlob(t, idx, "GlobWalk", tt, fsys, matches, err)
	}
}

func testStandardGlob(t *testing.T, idx int, fn string, tt MatchTest, fsys fs.FS, matches []string, err error) {
	if tt.isStandard {
		stdMatches, stdErr := fs.Glob(fsys, tt.pattern)
		if !compareSlices(matches, stdMatches) || !compareErrors(err, stdErr) {
			t.Errorf("#%v. %v(%#q) != fs.Glob(...). Got %#v, %v want %#v, %v", idx, fn, tt.pattern, matches, err, stdMatches, stdErr)
		}
	}
}

func TestFilepathGlob(t *testing.T) {
	doFilepathGlobTest(t)
}

func TestFilepathGlobWithFailOnIOErrors(t *testing.T) {
	doFilepathGlobTest(t, WithFailOnIOErrors())
}

func doFilepathGlobTest(t *testing.T, opts ...GlobOption) {
	glob := newGlob(opts...)
	fsys := os.DirFS("test")

	// The patterns are relative to the "test" sub-directory.
	defer func() {
		os.Chdir("..")
	}()
	os.Chdir("test")

	for idx, tt := range matchTests {
		if tt.testOnDisk {
			ttmod := tt
			ttmod.pattern = filepath.FromSlash(tt.pattern)
			ttmod.testPath = filepath.FromSlash(tt.testPath)
			testFilepathGlobWith(t, idx, ttmod, glob, opts, fsys)
		}
	}
}

func testFilepathGlobWith(t *testing.T, idx int, tt MatchTest, g *glob, opts []GlobOption, fsys fs.FS) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("#%v. FilepathGlob(%#q, %#v) panicked: %#v", idx, tt.pattern, opts, r)
		}
	}()

	matches, err := FilepathGlob(tt.pattern, opts...)
	verifyGlobResults(t, idx, "FilepathGlob", tt, g, fsys, matches, err)

	if tt.isStandard && len(opts) == 0 {
		stdMatches, stdErr := filepath.Glob(tt.pattern)
		if !compareSlices(matches, stdMatches) || !compareErrors(err, stdErr) {
			t.Errorf("#%v. FilepathGlob(%#q, %#v) != filepath.Glob(...). Got %#v, %v want %#v, %v", idx, tt.pattern, opts, matches, err, stdMatches, stdErr)
		}
	}
}

func verifyGlobResults(t *testing.T, idx int, fn string, tt MatchTest, g *glob, fsys fs.FS, matches []string, err error) {
	if g.failOnIOErrors {
		if tt.expectIOErr && err == nil {
			t.Errorf("#%v. %v(%#q, %#v) does not have an error, but should", idx, fn, tt.pattern, g)
		} else if !tt.expectIOErr && err != nil && err != tt.expectedErr {
			t.Errorf("#%v. %v(%#q, %#v) has error %v, but should not", idx, fn, tt.pattern, g, err)
		}
		return
	}

	numResults := tt.numResults
	if onWindows {
		numResults = tt.winNumResults
	}
	if len(matches) != numResults {
		t.Errorf("#%v. %v(%#q, %#v) = %#v - should have %#v results, got %#v", idx, fn, tt.pattern, g, matches, numResults, len(matches))
	}
	if inSlice(tt.testPath, matches) != tt.shouldMatch {
		if tt.shouldMatch {
			t.Errorf("#%v. %v(%#q, %#v) = %#v - doesn't contain %v, but should", idx, fn, tt.pattern, g, matches, tt.testPath)
		} else {
			t.Errorf("#%v. %v(%#q, %#v) = %#v - contains %v, but shouldn't", idx, fn, tt.pattern, g, matches, tt.testPath)
		}
	}
	if err != tt.expectedErr {
		t.Errorf("#%v. %v(%#q, %#v) has error %v, but should be %v", idx, fn, tt.pattern, g, err, tt.expectedErr)
	}
}

func TestGlobSorted(t *testing.T) {
	fsys := os.DirFS("test")
	expected := []string{"a", "abc", "abcd", "abcde", "abxbbxdbxebxczzx", "abxbbxdbxebxczzy", "axbxcxdxe", "axbxcxdxexxx", "a☺b"}
	matches, err := Glob(fsys, "a*")
	if err != nil {
		t.Errorf("Unexpected error %v", err)
		return
	}

	if len(matches) != len(expected) {
		t.Errorf("Glob returned %#v; expected %#v", matches, expected)
		return
	}
	for idx, match := range matches {
		if match != expected[idx] {
			t.Errorf("Glob returned %#v; expected %#v", matches, expected)
			return
		}
	}
}

func BenchmarkGlob(b *testing.B) {
	fsys := os.DirFS("test")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range matchTests {
			if tt.isStandard && tt.testOnDisk {
				Glob(fsys, tt.pattern)
			}
		}
	}
}

func BenchmarkGlobWalk(b *testing.B) {
	fsys := os.DirFS("test")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range matchTests {
			if tt.isStandard && tt.testOnDisk {
				GlobWalk(fsys, tt.pattern, func(p string, d fs.DirEntry) error {
					return nil
				})
			}
		}
	}
}

func BenchmarkGoGlob(b *testing.B) {
	fsys := os.DirFS("test")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, tt := range matchTests {
			if tt.isStandard && tt.testOnDisk {
				fs.Glob(fsys, tt.pattern)
			}
		}
	}
}

func compareErrors(a, b error) bool {
	if a == nil {
		return b == nil
	}
	return b != nil
}

func inSlice(s string, a []string) bool {
	for _, i := range a {
		if i == s {
			return true
		}
	}
	return false
}

func compareSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	diff := make(map[string]int, len(a))

	for _, x := range a {
		diff[x]++
	}

	for _, y := range b {
		if _, ok := diff[y]; !ok {
			return false
		}

		diff[y]--
		if diff[y] == 0 {
			delete(diff, y)
		}
	}

	return len(diff) == 0
}

func mkdirp(parts ...string) {
	dirs := path.Join(parts...)
	err := os.MkdirAll(dirs, 0755)
	if err != nil {
		log.Fatalf("Could not create test directories %v: %v\n", dirs, err)
	}
}

func touch(parts ...string) {
	filename := path.Join(parts...)
	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("Could not create test file %v: %v\n", filename, err)
	}
	f.Close()
}

func symlink(oldname, newname string) {
	// since this will only run on non-windows, we can assume "/" as path separator
	err := os.Symlink(oldname, newname)
	if err != nil && !os.IsExist(err) {
		log.Fatalf("Could not create symlink %v -> %v: %v\n", oldname, newname, err)
	}
}

func TestMain(m *testing.M) {
	// create the test directory
	mkdirp("test", "a", "b", "c")
	mkdirp("test", "a", "c")
	mkdirp("test", "abc")
	mkdirp("test", "axbxcxdxe", "xxx")
	mkdirp("test", "axbxcxdxexxx")
	mkdirp("test", "b")
	mkdirp("test", "e")

	// create test files
	touch("test", "a", "abc")
	touch("test", "a", "b", "c", "d")
	touch("test", "a", "c", "b")
	touch("test", "abc", "b")
	touch("test", "abcd")
	touch("test", "abcde")
	touch("test", "abxbbxdbxebxczzx")
	touch("test", "abxbbxdbxebxczzy")
	touch("test", "axbxcxdxe", "f")
	touch("test", "axbxcxdxe", "xxx", "f")
	touch("test", "axbxcxdxexxx", "f")
	touch("test", "axbxcxdxexxx", "fff")
	touch("test", "a☺b")
	touch("test", "b", "c")
	touch("test", "c")
	touch("test", "x")
	touch("test", "xxx")
	touch("test", "z")
	touch("test", "α")
	touch("test", "abc", "【test】.txt")

	touch("test", "e", "[")
	touch("test", "e", "]")
	touch("test", "e", "{")
	touch("test", "e", "}")
	touch("test", "e", "[]")

	if !onWindows {
		// these files/symlinks won't work on Windows
		touch("test", "-")
		touch("test", "]")
		touch("test", "e", "*")
		touch("test", "e", "**")
		touch("test", "e", "****")
		touch("test", "e", "?")
		touch("test", "e", "\\")

		symlink("../axbxcxdxe/", "test/b/symlink-dir")
		symlink("/tmp/nonexistant-file-20160902155705", "test/broken-symlink")
		symlink("a/b", "test/working-symlink")
	}

	os.Exit(m.Run())
}
