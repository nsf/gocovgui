package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/axw/gocov"
	"github.com/nsf/gothic"
)

type goPart struct {
	*gothic.Interpreter
	funcs           []*function
	prevsel         string
	xsourceview     string
	ysourceview     string
	gocovPath       string
	animationStop   chan bool
	animationWidget string
	sortBy          string
	sortOrder       string
}

type function struct {
	id          string
	name        string
	coverage    string
	file        string
	path        string
	sTotal      int
	sReached    int
	sPercentage float64
	statements  []statement

	// offset in bytes from the beginning of the file
	start int
	end   int
}

// gocovgui collects only unreached statements
type statement struct {
	// Offset in bytes from the beginning of the function.
	start int
	end   int

	// Cached offset in characters from the beginning of the function. We
	// can't calculate them on gocov update because it's pointless to read
	// files before we actually need them.
	startc int
	endc   int
}

func (s *statement) calculateCharOffset(data []byte) {
	// let's do it slow O(N*M) way
	b := 0
	c := 0
	for b < len(data) {
		_, size := utf8.DecodeRune(data[b:])
		if s.start == b {
			s.startc = c
		}
		if s.end == b {
			s.endc = c
			break
		}

		b += size
		c++
	}
}

func convertStatements(s []*gocov.Statement, offset int) []statement {
	var out []statement
	for _, s := range s {
		if s.Reached != 0 {
			continue
		}

		out = append(out, statement{
			start:  s.Start - offset,
			end:    s.End - offset,
			startc: -1,
			endc:   -1,
		})
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func percentage(n, len int) float64 {
	return float64(n) / float64(len) * 100.0
}

func (g *goPart) detailedError(plot string, details fmt.Stringer) {
	g.Set("detailtext", details.String())
	g.Eval(`
		set w .errdet
    		toplevel $w
    		wm title $w "GoCovGUI Error"
    		wm transient $w .
    		wm group $w .

		ttk::label $w.caption
		$w.caption config -anchor nw -justify left
		$w.caption config -text %{0%q}
		$w.caption config -font TkCaptionFont

		canvas $w.errimg -width 32 -height 32 -highlightthickness 0
		$w.errimg create oval 0 0 31 31 -fill red -outline black
		$w.errimg create line 9 9 23 23 -fill white -width 4
		$w.errimg create line 9 23 23 9 -fill white -width 4

		text $w.detail
		$w.detail insert end $detailtext
		$w.detail config -state disabled
		$w.detail config -wrap none
		$w.detail config -yscrollcommand "$w.detailv set"
		$w.detail config -xscrollcommand "$w.detailh set"
		ttk::scrollbar $w.detailv -command "$w.detail yview" -orient vertical
		ttk::scrollbar $w.detailh -command "$w.detail xview" -orient horizontal

		ttk::separator $w.sep
		ttk::frame $w.bf
		ttk::button $w.bf.ok -text OK -command "destroy $w"
		pack $w.bf.ok -padx 2m -pady 2m

		grid $w.errimg  $w.caption -          -sticky nwse -padx 2m -pady 2m
		grid $w.detail  -          $w.detailv -sticky nwse
		grid $w.detailh -          x          -sticky nwse -pady {0 2m}
		grid $w.sep     -          -          -sticky we
		grid $w.bf      -          -          -sticky nwse

		grid rowconfig    $w 1 -weight 1
		grid columnconfig $w 1 -weight 1

		grab $w
		tkwait window $w
	`, plot)
}

func (g *goPart) error(err interface{}) {
	g.Eval(`
		tk_messageBox -title "GoCovGUI Error" \
			-message "GoCovGUI Error" -icon error \
			-detail %{%q}
	`, err)
}

func (g *goPart) fatalError(err interface{}) {
	g.Eval(`
		tk_messageBox -title "GoCovGUI Fatal Error" \
			-message "GoCovGUI Fatal Error" -icon error \
			-detail %{%q}
		exit 1
	`, err)
}

// here we skip whitespace characters between \n and first non-space character
// on the next line, it simply looks better
func (g *goPart) highlightRangeNicely(c int, data []byte) {
	const fmt = `.f1.sourceview tag add red {1.0 +%{}chars} {1.0 +%{}chars}`
	skipping := false
	b := 0
	beg := c
	for b < len(data) {
		r, size := utf8.DecodeRune(data[b:])
		if !skipping {
			if r == '\n' {
				g.Eval(fmt, beg, c)
				skipping = true
			}
		} else {
			if !unicode.IsSpace(r) {
				beg = c
				skipping = false
			}
		}

		b += size
		c++
	}
	if !skipping {
		g.Eval(fmt, beg, c)
	}
}

func (g *goPart) TCL_selection_callback(ir *gothic.Interpreter) {
	var selection string
	g.EvalAs(&selection, ".f2.funcs selection")

	var fi int
	_, err := fmt.Sscanf(selection, "fi_%d", &fi)
	if err != nil {
		panic(err)
	}

	f := g.funcs[fi]
	g.prevsel = f.name

	data, err := ioutil.ReadFile(f.path)
	if err != nil {
		panic(err)
	}

	funcdata := data[f.start:f.end]
	g.Set("source", string(funcdata))
	g.Eval(`
		.f1.sourceview configure -state normal
		.f1.sourceview delete 1.0 end
		.f1.sourceview insert end $source
		.f1.sourceview configure -state disabled
	`)
	for _, s := range f.statements {
		if s.startc == -1 {
			s.calculateCharOffset(funcdata)
		}
		g.highlightRangeNicely(s.startc, funcdata[s.start:s.end])
	}

	if g.xsourceview != "" {
		g.Eval(".f1.sourceview xview moveto [lindex {%{}} 0]", g.xsourceview)
		g.Eval(".f1.sourceview yview moveto [lindex {%{}} 0]", g.ysourceview)
		g.xsourceview = ""
		g.ysourceview = ""
	}
}

func (g *goPart) TCL_update() {
	g.Eval(`
		.f2.funcs delete [.f2.funcs children {}]
		set covtext "Overall coverage: 0% (0/0)"
		set pathtext ""
	`)
	g.EvalAs(&g.xsourceview, ".f1.sourceview xview")
	g.EvalAs(&g.ysourceview, ".f1.sourceview yview")

	var outbuf bytes.Buffer
	var errbuf bytes.Buffer

	var cmd *exec.Cmd
	if len(os.Args) == 2 {
		cmd = exec.Command(g.gocovPath, "test", os.Args[1])
	} else {
		cmd = exec.Command(g.gocovPath, "test")
	}
	cmd.Stdout = &outbuf
	cmd.Stderr = &errbuf
	err := cmd.Run()
	if err != nil {
		if errbuf.Len() > 0 {
			g.detailedError("gocov test failure", &errbuf)
		} else {
			g.error(err)
		}
		return
	}

	result := struct{ Packages []*gocov.Package }{}
	err = json.Unmarshal(outbuf.Bytes(), &result)
	if err != nil {
		g.error(err)
		return
	}

	sel := ""

	// build functions
	g.funcs = g.funcs[:0]
	for _, pkg := range result.Packages {
		for _, fun := range pkg.Functions {
			statements := convertStatements(fun.Statements, fun.Start)
			r := len(fun.Statements) - len(statements)
			t := len(fun.Statements)
			p := percentage(r, t)
			coverage := fmt.Sprintf("%.2f%% (%d/%d)", p, r, t)
			file := fmt.Sprintf("%s/%s", pkg.Name, filepath.Base(fun.File))
			id := fmt.Sprintf("fi_%d", len(g.funcs))
			g.funcs = append(g.funcs, &function{
				id:          id,
				name:        fun.Name,
				coverage:    coverage,
				file:        file,
				path:        fun.File,
				sTotal:      t,
				sReached:    r,
				sPercentage: p,
				start:       fun.Start,
				end:         fun.End,
				statements:  statements,
			})

			if g.prevsel != "" && g.prevsel == fun.Name {
				sel = id
			}
		}
	}

	for _, f := range g.funcs {
		g.Eval(`.f2.funcs insert {} end -id %{} -values {%{%q} %{%q} %{%q}}`,
			f.id, f.name, f.file, f.coverage)
	}
	g.TCL_sort(g.sortBy, g.sortOrder)

	dir := filepath.Dir(g.funcs[0].path)
	g.Set("pathtext", dir)

	done := 0
	total := 0
	for _, f := range g.funcs {
		done += f.sReached
		total += f.sTotal
	}
	g.Set("covtext", fmt.Sprintf("Overall coverage: %.2f%% (%d/%d)",
		percentage(done, total), done, total))

	if sel == "" {
		sel = "fi_0"
	}
	g.Eval(".f2.funcs selection set %{}", sel)
}

func (g *goPart) TCL_sort(by, order string) {
	var image string
	var si sort.Interface
	var opposite string

	g.sortBy = by
	g.sortOrder = order

	sorted := make([]*function, len(g.funcs))
	copy(sorted, g.funcs)
	g.Eval(`
		# clean up images
		.f2.funcs heading name     -image img::arrowblank
		.f2.funcs heading file     -image img::arrowblank
		.f2.funcs heading coverage -image img::arrowblank
		.f2.funcs heading name     -command {go::sort name asc}
		.f2.funcs heading file     -command {go::sort file asc}
		.f2.funcs heading coverage -command {go::sort coverage asc}
	`)
	si = funcsSortCoverageDesc{sorted}
	if order == "desc" {
		opposite = "asc"
		image = "img::arrowdown"
	} else {
		image = "img::arrowup"
		opposite = "desc"
	}

	switch by {
	case "name":
		if order == "desc" {
			si = funcsSortNameDesc{sorted}
		} else {
			si = funcsSortNameAsc{sorted}
		}
	case "file":
		if order == "desc" {
			si = funcsSortFileDesc{sorted}
		} else {
			si = funcsSortFileAsc{sorted}
		}
	case "coverage":
		if order == "desc" {
			si = funcsSortCoverageDesc{sorted}
		} else {
			si = funcsSortCoverageAsc{sorted}
		}
	}
	sort.Sort(si)
	for i, f := range sorted {
		g.Eval(`.f2.funcs move %{} {} %{}`, f.id, i)
	}
	g.Eval(`
		.f2.funcs heading %{0} -command {go::sort %{0} %{1}}
		.f2.funcs heading %{0} -image %{2}
	`, by, opposite, image)
}

func (g *goPart) main() {
	g.Eval(mainCode)
	g.TCL_update()
}

type funcsSortBase []*function

func (f funcsSortBase) Len() int      { return len(f) }
func (f funcsSortBase) Swap(i, j int) { f[i], f[j] = f[j], f[i] }

type funcsSortNameAsc struct{ funcsSortBase }
type funcsSortNameDesc struct{ funcsSortBase }
type funcsSortFileAsc struct{ funcsSortBase }
type funcsSortFileDesc struct{ funcsSortBase }
type funcsSortCoverageAsc struct{ funcsSortBase }
type funcsSortCoverageDesc struct{ funcsSortBase }

func (f funcsSortNameAsc) Less(i, j int) bool {
	s := f.funcsSortBase
	return s[i].name < s[j].name
}

func (f funcsSortNameDesc) Less(i, j int) bool {
	s := f.funcsSortBase
	return s[i].name >= s[j].name
}

func (f funcsSortFileAsc) Less(i, j int) bool {
	s := f.funcsSortBase
	if s[i].file == s[j].file {
		return s[i].name < s[j].name
	}
	return s[i].file < s[j].file
}

func (f funcsSortFileDesc) Less(i, j int) bool {
	s := f.funcsSortBase
	if s[i].file == s[j].file {
		return s[i].name < s[j].name
	}
	return s[i].file > s[j].file
}

func (f funcsSortCoverageDesc) Less(i, j int) bool {
	s := f.funcsSortBase
	if s[i].sPercentage == s[j].sPercentage {
		return s[i].name < s[j].name
	}
	return s[i].sPercentage > s[j].sPercentage
}

func (f funcsSortCoverageAsc) Less(i, j int) bool {
	s := f.funcsSortBase
	if s[i].sPercentage == s[j].sPercentage {
		return s[i].name < s[j].name
	}
	return s[i].sPercentage < s[j].sPercentage
}

const mainCode = `
	wm deiconify .
	wm title . "GoCov GUI"

	# ---------------------------- paned window ----------------------------
	ttk::panedwindow .p -orient vertical
	ttk::frame .f1
	ttk::frame .f2
	.p add .f1 -weight 1
	.p add .f2 -weight 0

	# ---------------------------- arrow images ----------------------------
	image create bitmap img::arrowdown -data {
		#define bm_width 8
		#define bm_height 8
		static char bm_bits = {
			0x00, 0x00, 0x7f, 0x3e, 0x1c, 0x08, 0x00, 0x00
		}
	}
	image create bitmap img::arrowup -data {
		#define bm_width 8
		#define bm_height 8
		static char bm_bits = {
			0x00, 0x00, 0x08, 0x1c, 0x3e, 0x7f, 0x00, 0x00
		}
	}
	image create bitmap img::arrowblank -data {
		#define bm_width 8
		#define bm_height 8
		static char bm_bits = {
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00
		}
	}

	# ---------------------------- upper half ------------------------------
	set p .f1

	# source view
	text $p.sourceview
	$p.sourceview tag configure red -background "#FFCCCC"
	$p.sourceview configure -yscrollcommand "$p.sourceview_vscroll set"
	$p.sourceview configure -xscrollcommand "$p.sourceview_hscroll set"
	$p.sourceview configure -wrap none
	$p.sourceview configure -state disabled

	# source view scrollbars
	ttk::scrollbar $p.sourceview_vscroll -command "$p.sourceview yview" -orient vertical
	ttk::scrollbar $p.sourceview_hscroll -command "$p.sourceview xview" -orient horizontal

	# refresh button
	set bar $p.bar
	ttk::frame $bar
	ttk::button $bar.gocov -text "gocov test" -command go::update
	ttk::label $bar.path -relief sunken -padding {3 0} -textvariable pathtext
	ttk::label $bar.cov -relief sunken -padding {3 0} -textvariable covtext
	set covtext "Overall coverage: 0% (0/0)"

	# packing
	grid $bar.path $bar.cov $bar.gocov -sticky nwse

	grid rowconfigure    $bar $bar.path -weight 1
	grid columnconfigure $bar $bar.path -weight 1

	grid $p.bar                -                     -sticky nwse
	grid $p.sourceview         $p.sourceview_vscroll -sticky nwse
	grid $p.sourceview_hscroll x                     -sticky nwse

	grid rowconfigure    $p $p.sourceview -weight 1
	grid columnconfigure $p $p.sourceview -weight 1

	# ---------------------------- lower half ------------------------------
	set p .f2

	# functions list
	ttk::treeview $p.funcs -yscrollcommand "$p.funcs_vscroll set"
	$p.funcs configure -columns {name file coverage}
	$p.funcs configure -selectmode browse
	$p.funcs configure -show headings
	$p.funcs heading name     -text "Function"
	$p.funcs heading file     -text "File"
	$p.funcs heading coverage -text "Coverage"
	$p.funcs column  name     -minwidth 100 -width 300
	$p.funcs column  file     -minwidth 100 -width 300 -stretch false
	$p.funcs column  coverage -minwidth 120 -width 120 -stretch false

	# functions scrollbar
	ttk::scrollbar $p.funcs_vscroll -command "$p.funcs yview" -orient vertical

	# packing
	grid $p.funcs $p.funcs_vscroll -sticky nwse
	grid rowconfigure $p 0 -weight 1
	grid columnconfigure $p 0 -weight 1

	# ---------------------------- paned window pack------------------------
	pack .p -expand true -fill both

	# ---------------------------- bindings --------------------------------
	bind .f2.funcs <<TreeviewSelect>> go::selection_callback
`

func (g *goPart) findGocov() {
	path, err := exec.LookPath("gocov")
	if err == nil {
		g.gocovPath = path
		return
	}

	path = filepath.Join(runtime.GOROOT(), "bin", "gocov")
	if fileExists(path) {
		g.gocovPath = path
		return
	}

	goroot := os.Getenv("GOROOT")
	if goroot != "" {
		path = filepath.Join(goroot, "bin", "gocov")
		if fileExists(path) {
			g.gocovPath = path
			return
		}
	}

	gobin := os.Getenv("GOBIN")
	if gobin != "" {
		path = filepath.Join(gobin, "gocov")
		if fileExists(path) {
			g.gocovPath = path
			return
		}
	}

	gopaths := filepath.SplitList(os.Getenv("GOPATH"))
	for _, path := range gopaths {
		path = filepath.Join(path, "bin", "gocov")
		if fileExists(path) {
			g.gocovPath = path
			return
		}
	}
}

func main() {
	g := goPart{
		sortBy:    "coverage",
		sortOrder: "desc",
	}
	g.findGocov()
	g.Interpreter = gothic.NewInterpreter("wm withdraw .")
	g.RegisterCommands("go", &g)
	g.ErrorFilter(func(err error) error {
		if err != nil {
			g.Eval("tk_messageBox -title Error -icon error -message %{%q}", err)
		}
		return err
	})

	if g.gocovPath == "" {
		// oops, still haven't found the gocov tool, let's show
		// a nice gui with various choices
		g.notFound()
	} else {
		g.main()
	}
	<-g.Done
}
