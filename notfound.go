// this file contains the code which is executed if the gocov binary wasn't found
// the idea is to display a nice GUI to a user with an option to execute `go get`

package main

import (
	"time"
	"os/exec"
	"bytes"
)

func (g *go_part) animation_ticker() {
	for {
		time.Sleep(100 * time.Millisecond)
		select {
		case <-g.animation_stop:
			g.animation_stop = nil
			return
		default:
		}
		g.Eval(`
			set image [%{0} cget -image]
			set idx -1
			scan [$image cget -format] "GIF -index %d" idx
			if {[catch {$image config -format "GIF -index [incr idx]"}]} {
				$image config -format "GIF -index 0"
			}
			%{0} config -image $image
		`, g.animation_widget)
	}
}

func (g *go_part) TCL_start_animation_goroutine(w string) {
	g.animation_stop = make(chan bool, 1)
	g.animation_widget = w
	go g.animation_ticker()
}

func (g *go_part) TCL_stop_animation_goroutine() {
	select {
	case g.animation_stop <- true:
	default:
	}
}

// stops animations and brings the "Go get" button back to its initial state
func (g *go_part) go_get_gocov_done() {
	g.TCL_stop_animation_goroutine()
	g.Eval(`
		%{0} config -text "Get gocov" -image {} -state normal
	`, g.animation_widget)
}

// Fires a goroutine which will invoke "go get -u github.com/axw/gocov/gocov"
// and then perform another try to find the gocov binary. If it succeeds, the
// goroutine will invoke g.main(), otherwise it shows a fatal error message
// and does "exit".
func (g *go_part) TCL_go_get_gocov() {
	go g.go_get_gocov()
}

func (g *go_part) go_get_gocov() {
	var errbuf bytes.Buffer
	cmd := exec.Command("go", "get", "-u", "github.com/axw/gocov/gocov")
	cmd.Stderr = &errbuf
	err := cmd.Run()
	if err != nil {
		g.go_get_gocov_done()
		if errbuf.Len() > 0 {
			g.fatal_error(&errbuf)
		} else {
			g.fatal_error(err)
		}
	}

	// try to find the gocov tool again
	g.find_gocov()
	if g.gocov_path == "" {
		// no luck :(
		g.go_get_gocov_done()
		g.fatal_error(`Unable to find the gocov binary after running "go get"`)
	}

	// all good, let's proceed
	g.Eval(`destroy .nf`)
	g.main()
}

const not_found_msg = `GoCovGUI failed to find the gocov tool itself, it ` +
	`checks the following paths: $PATH, $GOROOT/bin, $GOBIN and $GOPATH/bin. ` +
	`However, gocovgui can "go get" the tool for, just click the ` +
	`"Get gocov" button.`

const not_found_code = `
	set progressimg [image create photo -format GIF -data {
		R0lGODlhEAAQAPIAAP///wAAAMLCwkJCQgAAAGJiYoKCgpKSkiH/C05FVFNDQVBFMi4wAwEAAAAh
		/hpDcmVhdGVkIHdpdGggYWpheGxvYWQuaW5mbwAh+QQJCgAAACwAAAAAEAAQAAADMwi63P4wyklr
		E2MIOggZnAdOmGYJRbExwroUmcG2LmDEwnHQLVsYOd2mBzkYDAdKa+dIAAAh+QQJCgAAACwAAAAA
		EAAQAAADNAi63P5OjCEgG4QMu7DmikRxQlFUYDEZIGBMRVsaqHwctXXf7WEYB4Ag1xjihkMZsiUk
		KhIAIfkECQoAAAAsAAAAABAAEAAAAzYIujIjK8pByJDMlFYvBoVjHA70GU7xSUJhmKtwHPAKzLO9
		HMaoKwJZ7Rf8AYPDDzKpZBqfvwQAIfkECQoAAAAsAAAAABAAEAAAAzMIumIlK8oyhpHsnFZfhYum
		CYUhDAQxRIdhHBGqRoKw0R8DYlJd8z0fMDgsGo/IpHI5TAAAIfkECQoAAAAsAAAAABAAEAAAAzII
		unInK0rnZBTwGPNMgQwmdsNgXGJUlIWEuR5oWUIpz8pAEAMe6TwfwyYsGo/IpFKSAAAh+QQJCgAA
		ACwAAAAAEAAQAAADMwi6IMKQORfjdOe82p4wGccc4CEuQradylesojEMBgsUc2G7sDX3lQGBMLAJ
		ibufbSlKAAAh+QQJCgAAACwAAAAAEAAQAAADMgi63P7wCRHZnFVdmgHu2nFwlWCI3WGc3TSWhUFG
		xTAUkGCbtgENBMJAEJsxgMLWzpEAACH5BAkKAAAALAAAAAAQABAAAAMyCLrc/jDKSatlQtScKdce
		CAjDII7HcQ4EMTCpyrCuUBjCYRgHVtqlAiB1YhiCnlsRkAAAOwAAAAAAAAAAAA==
	}]

	proc gocov_get_gocov {w} {
		global progressimg
		$w.bget config -image $progressimg
		$w.bget config -state disabled
		go::start_animation_goroutine $w.bget
		go::go_get_gocov
	}

	set w .nf
	toplevel $w
	wm title $w GoCovGUI
	wm protocol $w WM_DELETE_WINDOW {exit}

	ttk::label $w.caption
	$w.caption config -anchor nw -justify left
	$w.caption config -text "Gocov not found"
	$w.caption config -font TkCaptionFont

	# TODO: http://wiki.tcl.tk/10031
	ttk::label $w.detail
	$w.detail config -justify left -anchor nw -wraplength 500
	$w.detail config -text $errmsg

	canvas $w.errimg -width 32 -height 32 -highlightthickness 0
	$w.errimg create oval 0 0 31 31 -fill red -outline black
	$w.errimg create line 9 9 23 23 -fill white -width 4
	$w.errimg create line 9 23 23 9 -fill white -width 4

	ttk::separator $w.sep
	ttk::button $w.bget -text "Get gocov" -command "gocov_get_gocov $w"
	ttk::button $w.bquit -text "Quit" -command exit

	grid $w.errimg $w.caption -       -        -sticky nwse -padx 2m -pady 2m
	grid ^         $w.detail  -       -        -sticky nws  -padx 2m -pady {0 2m}
	grid $w.sep    -          -       -        -sticky we
	grid x         x          $w.bget $w.bquit -sticky nwse -padx 2m -pady 2m

	grid columnconfigure $w 1 -weight 1
	grid rowconfigure    $w 1 -weight 1

	bind $w <Destroy> {
		go::stop_animation_goroutine
	}
`

func (g *go_part) not_found() {
	g.Set("errmsg", not_found_msg)
	g.Eval(not_found_code)
}
