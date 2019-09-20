package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/jessevdk/go-flags"
)

/*
 * Sketch names must start with a letter or number, followed by letters,
 * numbers, dashes, dots and underscores. Maximum length is 63 characters.
 */
var line1r = regexp.MustCompile(`^(#line\s*1).*\/([a-zA-Z0-9_-]*)\.(ino|pde)`)
var loopre = regexp.MustCompile(`\s*void\s+(loop[a-zA-Z0-9_]*)\(\s*(void)?\s*\)\s*(;|\s*)`)
var setupre = regexp.MustCompile(`\s*void\s+(setup[a-zA-Z0-9_]*)\(\s*(void)?\s*\)\s*(;|\s*)`)
var insert = regexp.MustCompile(`769d20fcd7a0eedaf64270f591438b01`)

/*
 * Command line arguments
 */

var opts struct {
	OutputDir  string `short:"o" description:"Output directory of post processed sketch cpp file"`
	BuildDir   string `short:"b" description:"Sketch build directory" required:"true"`
	SketchName string `short:"s" description:"Sketch name" required:"true"`
	Core       string `short:"c" description:"Core to build for" required:"true"`
	Variant    string `short:"v" description:"Variant to build for" required:"true"`
}

/*
 * Structure to hold Sketch variables
 */
type sketch struct {
	name            string
	sketchStartLine int
	setupName       string
	setupLine       int
	loopName        string
	loopLine        int
	mangled         bool
}

/* Sketches array */
var sketches []sketch

func printLines(lines []string) {
	for _, line := range lines {
		fmt.Println(line)
	}
}

// writeLines writes the lines to the given file.
func writeLines(lines []string, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	w := bufio.NewWriter(file)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

// readLines reads a whole file into memory
// and returns a slice of its lines.
func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())

	}
	return lines, scanner.Err()
}

func main() {
	numSketches := 0
	var validSketches []sketch

	_, err := flags.Parse(&opts)

	if err != nil {
		os.Exit(2) // panic(err)
	}

	lines, err := readLines(opts.BuildDir + opts.SketchName + ".cpp")

	if err != nil {
		log.Fatalf("readLines: %s", err)
	}

	/* Get sketch names */
	for i, line := range lines {
		match := line1r.FindStringSubmatch(line)
		if len(match) > 0 {
			var s = sketch{match[2], i, "", 0, "", 0, false}
			numSketches++
			sketches = append(sketches, s)
		}
	}

	/* Collect setup line number and name */
	numSketches = -1
	for i, line := range lines {
		if line1r.MatchString(line) {
			numSketches++
		}
		match := setupre.FindStringSubmatch(line)
		if len(match) > 0 {
			if match[3] == ";" {
			} else {
				sketches[numSketches].setupLine = i
				sketches[numSketches].setupName = match[1]
			}
		}
	}

	/* Collect loop line number and name */

	numSketches = -1
	for i, line := range lines {
		if line1r.MatchString(line) {
			numSketches++
		}
		match := loopre.FindStringSubmatch(line)
		if len(match) > 0 {
			if match[3] == ";" {
			} else {
				sketches[numSketches].loopLine = i
				sketches[numSketches].loopName = match[1]
			}
		}
	}

	/*
	 * Only keep the Sketches that have a matching setup/loop tuple
	 */
	for _, s := range sketches {
		if s.loopName != "" && s.setupName != "" {
			validSketches = append(validSketches, s)
		}
	}

	/*
	 * in EMT, all setup and loop tuples in a single Sketch become a loo.
	 * Each setup and loop tuple therefor need a unique name. This loop will change the loop and setup
	 * into setup<Sketch name> and loop<Sketch name> and mark it as mangeled for later use when generationg
	 * main.cpp
	 */

	for i, s := range validSketches {
		/* Valid sketch. Figure out if we need to get setup and loop a different name */
		if s.loopName == "loop" && s.setupName == "setup" {
			validSketches[i].mangled = true
			validSketches[i].loopName = s.loopName + s.name
			validSketches[i].setupName = s.setupName + s.name
		}
	}

	insertedLines := 0

	for _, s := range validSketches {
		if s.mangled {
			lines = append(lines, make([]string, 4)...)
			copy(lines[s.sketchStartLine+insertedLines+4:], lines[s.sketchStartLine+insertedLines:])
			lines[s.sketchStartLine+insertedLines] = "#undef loop"
			lines[s.sketchStartLine+insertedLines+1] = "#undef setup"
			lines[s.sketchStartLine+insertedLines+2] = "#define setup " + s.setupName
			lines[s.sketchStartLine+insertedLines+3] = "#define loop " + s.loopName
			insertedLines += 4
		}
	}

	/* Write out sketch cpp code */
	if err := writeLines(lines, opts.BuildDir+"/"+opts.SketchName+".cpp"); err != nil {
		log.Fatalf("writeLines: %s", err)
	}

	insertLine := 0
	/* On to the template to generate main.cpp */
	dirSelf, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	lines, err = readLines(dirSelf + "/templates/" + opts.Core + "/" + opts.Variant + ".main.template")
	if err != nil {
		log.Fatalf("Read template: %s", err)
	}
	for i, line := range lines {
		if !insert.MatchString(line) {
			continue
		}
		insertLine = i + 1
		break
	}
	insertLen := (len(validSketches) * 3) + 6
	lines = append(lines, make([]string, insertLen)...)
	copy(lines[insertLine+insertLen:], lines[insertLine:])

	for _, s := range validSketches {
		lines[insertLine] = "extern void " + s.setupName + "();"
		lines[insertLine+1] = "extern void " + s.loopName + "();"
		insertLine += 2
	}

	lines[insertLine] = ""
	insertLine++
	lines[insertLine] = "#define NUM_SKETCHES " + strconv.Itoa(len(validSketches))
	insertLine++
	lines[insertLine] = "void (*func_ptr[NUM_SKETCHES][" + strconv.Itoa(len(validSketches)*2) + "])(void) = {"
	insertLine++

	for i, s := range validSketches {
		lines[insertLine] = "\t{" + s.setupName + " ," + s.loopName + "}"

		if i+1 != len(validSketches) {
			lines[insertLine] += ","
		}
		insertLine++
	}
	lines[insertLine] = "};"
	insertLine++

	lines[insertLine] = "const char *taskNames[] = {"
	insertLine++
	lines[insertLine] = "\t"
	for i, s := range validSketches {
		lines[insertLine] += "\"" + s.loopName + "\""

		if i+1 != len(validSketches) {
			lines[insertLine] += ", "
		}
		// insertLine++
	}
	lines[insertLine] += "};"

	/* Write out sketch cpp code */
	if err := writeLines(lines, opts.BuildDir+"/main.cpp"); err != nil {
		log.Fatalf("writeLines: %s", err)
	}
}
