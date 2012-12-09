package main

import (
	"github.com/axw/gocov"
	"github.com/nsf/gothic"
	"os/exec"
	"bytes"
	"io/ioutil"
	"encoding/json"
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
	$p.sourceview tag configure red -background "#FF7878"
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
	$p.funcs column  coverage -minwidth 110 -width 110 -stretch false

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
	start int
	end int
	statements []*gocov.Statement
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

func gocov_test_error(ir *gothic.Interpreter, err error) {
	ir.Eval(`tk_messageBox -title "gocov test error" -icon error -message %{%q}`, err)
}

func reached(f *gocov.Function) int {
	i := 0
	for _, s := range f.Statements {
		if s.Reached != 0 {
			i++
		}
	}
	return i
}

func percentage(n, len int) float64 {
	return float64(n) / float64(len) * 100.0
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

	ir.Set("source", string(data[f.start:f.end]))
	ir.Eval(`
		.f1.sourceview configure -state normal
		.f1.sourceview delete 1.0 end
		.f1.sourceview insert end $source
		.f1.sourceview configure -state disabled
	`)
	for _, s := range f.statements {
		if s.Reached != 0 {
			continue
		}
		ls, le := s.Start - f.start, s.End - f.start
		ir.Eval(`.f1.sourceview tag add red {1.0 +%{}chars} {1.0 +%{}chars}`, ls, le)
	}

	if xsourceview != "" {
		ir.Eval(".f1.sourceview xview moveto [lindex {%{}} 0]", xsourceview)
		ir.Eval(".f1.sourceview yview moveto [lindex {%{}} 0]", ysourceview)
		xsourceview = ""
		ysourceview = ""
	}
}

func gocov_test(ir *gothic.Interpreter) {
	var buf bytes.Buffer
	cmd := exec.Command("gocov", "test")
	cmd.Stdout = &buf
	err := cmd.Run()
	if err != nil {
		gocov_test_error(ir, err)
		return
	}

	result := struct{ Packages []*gocov.Package }{}
	err = json.Unmarshal(buf.Bytes(), &result)
	if err != nil {
		gocov_test_error(ir, err)
		return
	}

	sel := ""

	// build functions
	funcs = funcs[:0]
	for _, pkg := range result.Packages {
		for _, fun := range pkg.Functions {
			r := reached(fun)
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
				statements: fun.Statements,
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
