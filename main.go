package main

import (
	"github.com/axw/gocov"
	"github.com/nsf/gothic"
	"os/exec"
	"bytes"
	"io/ioutil"
	"encoding/json"
	"unicode/utf8"
	"fmt"
	"path/filepath"
	"sort"
)

const init_code = `
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
	ttk::frame $p.bar
	ttk::button $p.bar.gocov -text "gocov test" -command gocov_update
	ttk::label $p.bar.path -relief sunken -padding {3 0} -textvariable pathtext
	ttk::label $p.bar.cov -relief sunken -padding {3 0} -textvariable covtext

	# packing
	grid $p.bar.path -column 0 -row 0 -sticky nwse
	grid $p.bar.cov -column 1 -row 0 -sticky nwse
	grid $p.bar.gocov -column 2 -row 0 -sticky nwse
	grid rowconfigure $p.bar 0 -weight 1
	grid columnconfigure $p.bar 0 -weight 1

	grid $p.bar                -column 0 -row 0 -columnspan 2 -sticky nwse
	grid $p.sourceview         -column 0 -row 1 -sticky nwse
	grid $p.sourceview_vscroll -column 1 -row 1 -sticky nwse
	grid $p.sourceview_hscroll -column 0 -row 2 -sticky nwse
	grid rowconfigure $p 1 -weight 1
	grid columnconfigure $p 0 -weight 1

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
	$p.funcs column  name     -minwidth 200 -width 400
	$p.funcs column  file     -minwidth 100 -width 200 -stretch false
	$p.funcs column  coverage -minwidth 120 -width 120 -stretch false

	# functions scrollbar
	ttk::scrollbar $p.funcs_vscroll -command "$p.funcs yview" -orient vertical

	# packing
	grid $p.funcs -column 0 -row 0 -sticky nwse
	grid $p.funcs_vscroll -column 1 -row 0 -sticky nwse
	grid rowconfigure $p 0 -weight 1
	grid columnconfigure $p 0 -weight 1

	# ---------------------------- paned window pack------------------------
	pack .p -expand true -fill both

	# ---------------------------- bindings --------------------------------
	bind .f2.funcs <<TreeviewSelect>> gocov_selection
`

type function struct {
	id string
	name string
	coverage string
	file string
	path string
	s_total int
	s_reached int
	s_percentage float64
	statements []statement

	// offset in bytes from the beginning of the file
	start int
	end int
}

// gocovgui collects only unreached statements
type statement struct {
	// Offset in bytes from the beginning of the function.
	start int
	end int

	// Cached offset in characters from the beginning of the function.
	// We can't calculate them on gocov update because it's pointless
	// to read files before we actually need them.
	startc int
	endc int
}

func (s *statement) calculate_char_offset(data []byte) {
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
		c += 1
		b += size
	}
}

type funcs_sort_base []*function
func (f funcs_sort_base) Len() int { return len(f) }
func (f funcs_sort_base) Swap(i, j int) { f[i], f[j] = f[j], f[i] }

type funcs_sort_name_asc struct { funcs_sort_base }
type funcs_sort_name_desc struct { funcs_sort_base }
type funcs_sort_file_asc struct { funcs_sort_base }
type funcs_sort_file_desc struct { funcs_sort_base }
type funcs_sort_coverage_asc struct { funcs_sort_base }
type funcs_sort_coverage_desc struct { funcs_sort_base }

func (f funcs_sort_name_asc) Less(i, j int) bool {
	s := f.funcs_sort_base
	return s[i].name < s[j].name
}
func (f funcs_sort_name_desc) Less(i, j int) bool {
	s := f.funcs_sort_base
	return s[i].name >= s[j].name
}
func (f funcs_sort_file_asc) Less(i, j int) bool {
	s := f.funcs_sort_base
	if s[i].file == s[j].file {
		return s[i].name < s[j].name
	}
	return s[i].file < s[j].file
}
func (f funcs_sort_file_desc) Less(i, j int) bool {
	s := f.funcs_sort_base
	if s[i].file == s[j].file {
		return s[i].name < s[j].name
	}
	return s[i].file > s[j].file
}
func (f funcs_sort_coverage_desc) Less(i, j int) bool {
	s := f.funcs_sort_base
	if s[i].s_percentage == s[j].s_percentage {
		return s[i].name < s[j].name
	}
	return s[i].s_percentage > s[j].s_percentage
}
func (f funcs_sort_coverage_asc) Less(i, j int) bool {
	s := f.funcs_sort_base
	if s[i].s_percentage == s[j].s_percentage {
		return s[i].name < s[j].name
	}
	return s[i].s_percentage < s[j].s_percentage
}

var (
	funcs []*function
	prevsel string
	xsourceview string
	ysourceview string
)

func gocov_test_error(ir *gothic.Interpreter, err string) {
	ir.Eval(`tk_messageBox -title "gocov test error" -icon error -message %{%q}`, err)
}

func convert_statements(s []*gocov.Statement, offset int) []statement {
	out := make([]statement, 0)
	for _, s := range s {
		if s.Reached != 0 {
			continue
		}

		out = append(out, statement{
			start: s.Start - offset,
			end: s.End - offset,
			startc: -1,
			endc: -1,
		})
	}
	return out
}

func percentage(n, len int) float64 {
	return float64(n) / float64(len) * 100.0
}

// here we skip whitespace characters between \n and first non-space character
// on the next line, it simply looks better
func highlight_range_nicely(ir *gothic.Interpreter, c int, data []byte) {
	const fmt = `.f1.sourceview tag add red {1.0 +%{}chars} {1.0 +%{}chars}`
	skipping := false
	b := 0
	beg := c
	for b < len(data) {
		r, size := utf8.DecodeRune(data[b:])
		if !skipping {
			if r == '\n' {
				ir.Eval(fmt, beg, c)
				skipping = true
			}
		} else {
			switch r {
			case ' ', '\t', '\r', '\n':
			default:
				beg = c
				skipping = false
			}
		}
		c += 1
		b += size
	}
	if !skipping {
		ir.Eval(fmt, beg, c)
	}
}

func gocov_selection(ir *gothic.Interpreter) {
	var selection string
	ir.EvalAs(&selection, ".f2.funcs selection")

	var fi int
	_, err := fmt.Sscanf(selection, "fi_%d", &fi)
	if err != nil {
		panic(err)
	}

	f := funcs[fi]
	prevsel = f.name

	data, err := ioutil.ReadFile(f.path)
	if err != nil {
		panic(err)
	}

	funcdata := data[f.start:f.end]
	ir.Set("source", string(funcdata))
	ir.Eval(`
		.f1.sourceview configure -state normal
		.f1.sourceview delete 1.0 end
		.f1.sourceview insert end $source
		.f1.sourceview configure -state disabled
	`)
	for _, s := range f.statements {
		if s.startc == -1 {
			s.calculate_char_offset(funcdata)
		}
		highlight_range_nicely(ir, s.startc, funcdata[s.start:s.end])
	}

	if xsourceview != "" {
		ir.Eval(".f1.sourceview xview moveto [lindex {%{}} 0]", xsourceview)
		ir.Eval(".f1.sourceview yview moveto [lindex {%{}} 0]", ysourceview)
		xsourceview = ""
		ysourceview = ""
	}
}

func gocov_test(ir *gothic.Interpreter) {
	var outbuf bytes.Buffer
	var errbuf bytes.Buffer

	cmd := exec.Command("gocov", "test")
	cmd.Stdout = &outbuf
	cmd.Stderr = &errbuf
	err := cmd.Run()
	if err != nil {
		if errbuf.Len() > 0 {
			gocov_test_error(ir, errbuf.String())
		} else {
			gocov_test_error(ir, err.Error())
		}
		return
	}

	result := struct{ Packages []*gocov.Package }{}
	err = json.Unmarshal(outbuf.Bytes(), &result)
	if err != nil {
		gocov_test_error(ir, err.Error())
		return
	}

	sel := ""

	// build functions
	funcs = funcs[:0]
	for _, pkg := range result.Packages {
		for _, fun := range pkg.Functions {
			statements := convert_statements(fun.Statements, fun.Start)
			r := len(fun.Statements) - len(statements)
			t := len(fun.Statements)
			p := percentage(r, t)
			name := fmt.Sprintf("%s.%s", pkg.Name, fun.Name)
			coverage := fmt.Sprintf("%.2f%% (%d/%d)", p, r, t)
			file := fmt.Sprintf("%s/%s", pkg.Name, filepath.Base(fun.File))
			id := fmt.Sprintf("fi_%d", len(funcs))
			funcs = append(funcs, &function{
				id: id,
				name: name,
				coverage: coverage,
				file: file,
				path: fun.File,
				s_total: t,
				s_reached: r,
				s_percentage: p,
				start: fun.Start,
				end: fun.End,
				statements: statements,
			})

			if prevsel != "" && prevsel == name {
				sel = id
			}
		}
	}

	for _, f := range funcs {
		ir.Eval(`.f2.funcs insert {} end -id %{} -values {%{%q} %{%q} %{%q}}`,
			f.id, f.name, f.file, f.coverage)
	}
	gocov_sort(ir, "coverage", "desc")

	dir := filepath.Dir(funcs[0].path)
	ir.Set("pathtext", dir)

	done := 0
	total := 0
	for _, f := range funcs {
		done += f.s_reached
		total += f.s_total
	}
	ir.Set("covtext", fmt.Sprintf("Overall coverage: %.2f%% (%d/%d)",
		percentage(done, total), done, total))

	if sel == "" {
		sel = "fi_0"
	}
	ir.Eval(".f2.funcs selection set %{}", sel)
}

func gocov_sort(ir *gothic.Interpreter, by, order string) {
	var image string
	var si sort.Interface
	var opposite string

	sorted := make([]*function, len(funcs))
	copy(sorted, funcs)
	ir.Eval(`
		# clean up images
		.f2.funcs heading name     -image img::arrowblank
		.f2.funcs heading file     -image img::arrowblank
		.f2.funcs heading coverage -image img::arrowblank
		.f2.funcs heading name     -command {gocov_sort name asc}
		.f2.funcs heading file     -command {gocov_sort file asc}
		.f2.funcs heading coverage -command {gocov_sort coverage asc}
	`)
	si = funcs_sort_coverage_desc{sorted}
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
			si = funcs_sort_name_desc{sorted}
		} else {
			si = funcs_sort_name_asc{sorted}
		}
	case "file":
		if order == "desc" {
			si = funcs_sort_file_desc{sorted}
		} else {
			si = funcs_sort_file_asc{sorted}
		}
	case "coverage":
		if order == "desc" {
			si = funcs_sort_coverage_desc{sorted}
		} else {
			si = funcs_sort_coverage_asc{sorted}
		}
	}
	sort.Sort(si)
	for i, f := range sorted {
		ir.Eval(`.f2.funcs move %{} {} %{}`, f.id, i)
	}
	ir.Eval(`
		.f2.funcs heading %{0} -command {gocov_sort %{0} %{1}}
		.f2.funcs heading %{0} -image %{2}
	`, by, opposite, image)
}

func gocov_update(ir *gothic.Interpreter) {
	ir.Eval(`
		.f2.funcs delete [.f2.funcs children {}]
	`)
	ir.EvalAs(&xsourceview, ".f1.sourceview xview")
	ir.EvalAs(&ysourceview, ".f1.sourceview yview")
	gocov_test(ir)
}

func main() {
	ir := gothic.NewInterpreter(init_code)
	ir.RegisterCommand("gocov_selection", func() {
		gocov_selection(ir)
	})
	ir.RegisterCommand("gocov_update", func() {
		gocov_update(ir)
	})
	ir.RegisterCommand("gocov_sort", func(by, order string) {
		gocov_sort(ir, by, order)
	})
	ir.ErrorFilter(func(err error) error {
		if err != nil {
			ir.Eval("tk_messageBox -title Error -icon error -message %{%q}", err)
		}
		return err
	})
	gocov_test(ir)
	<-ir.Done
}
