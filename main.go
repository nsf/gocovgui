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
)

const red = `#FF7878`
const yellow = `#FFDF52`

const init_code = `
	wm title . "GoCov GUI"

	# ---------------------------- paned window ----------------------------
	ttk::panedwindow .p -orient vertical
	ttk::frame .f1
	ttk::frame .f2
	.p add .f1 -weight 1
	.p add .f2 -weight 0

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
	ttk::button $p.bar.gocov -text "gocov test" -command gocovtest
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
	$p.funcs configure -columns {function file coverage}
	$p.funcs configure -selectmode browse
	$p.funcs configure -show headings
	$p.funcs heading function -text "Function"
	$p.funcs heading file -text "File"
	$p.funcs heading coverage -text "Coverage"
	$p.funcs column function -minwidth 200 -width 400
	$p.funcs column file -minwidth 100 -width 200 -stretch false
	$p.funcs column coverage -minwidth 110 -width 110 -stretch false

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
	bind .f2.funcs <<TreeviewSelect>> gocovsel
`

var (
	current []*gocov.Package
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

	var pi, fi int
	_, err := fmt.Sscanf(selection, "f_%d_%d", &pi, &fi)
	if err != nil {
		panic(err)
	}

	f := current[pi].Functions[fi]
	prevsel = fmt.Sprintf("%s.%s", current[pi].Name, f.Name)

	data, err := ioutil.ReadFile(f.File)
	if err != nil {
		panic(err)
	}

	ir.Set("source", string(data[f.Start:f.End]))
	ir.Eval(`
		.f1.sourceview configure -state normal
		.f1.sourceview delete 1.0 end
		.f1.sourceview insert end $source
		.f1.sourceview configure -state disabled
	`)
	for _, s := range f.Statements {
		if s.Reached != 0 {
			continue
		}
		ls, le := s.Start - f.Start, s.End - f.Start
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
	current = result.Packages
	for pi, p := range result.Packages {
		for fi, f := range p.Functions {
			r := reached(f)
			n := len(f.Statements)
			fun := fmt.Sprintf("%s.%s", p.Name, f.Name)
			cov := fmt.Sprintf("%.2f%% (%d/%d)", percentage(r, n), r, n)
			file := fmt.Sprintf("%s/%s", p.Name, filepath.Base(f.File))
			id := fmt.Sprintf("f_%d_%d", pi, fi)
			if prevsel != "" && prevsel == fun {
				sel = id
			}
			ir.Eval(`.f2.funcs insert {} end -id %{} -values {%{%q} %{%q} %{%q}}`,
				id, fun, file, cov)
		}
	}

	dir := filepath.Dir(current[0].Functions[0].File)
	ir.Set("pathtext", dir)

	done := 0
	total := 0
	for _, p := range result.Packages {
		for _, f := range p.Functions {
			done += reached(f)
			total += len(f.Statements)
		}
	}
	ir.Set("covtext", fmt.Sprintf("Overall coverage: %.2f%% (%d/%d)",
		percentage(done, total), done, total))

	if sel == "" {
		sel = "f_0_0"
	}
	ir.Eval(".f2.funcs selection set %{}", sel)
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
	ir.RegisterCommand("gocovsel", func() {
		gocov_selection(ir)
	})
	ir.RegisterCommand("gocovtest", func() {
		gocov_update(ir)
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
