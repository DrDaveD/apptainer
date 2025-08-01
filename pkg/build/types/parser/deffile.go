// Copyright (c) Contributors to the Apptainer project, established as
//   Apptainer a Series of LF Projects LLC.
//   For website terms of use, trademark policy, privacy policy and other
//   project policies see https://lfprojects.org/policies
// Copyright (c) 2018-2022, Sylabs Inc. All rights reserved.
// This software is licensed under a 3-clause BSD license. Please consult the
// LICENSE.md file distributed with the sources of this project regarding your
// rights to use or distribute this software.

package parser

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/apptainer/apptainer/pkg/build/types"
)

var (
	errInvalidSection  = errors.New("invalid section(s) specified")
	errEmptyDefinition = errors.New("empty definition file")
	// Match space but not within double quotes
	fileSplitter = regexp.MustCompile(`([^\s"']*{{\s*\w+\s*}}*[^\s{}"']*)+|([^\s"']+|"([^"]*)"|'([^']*))`)
)

// InvalidSectionError records an error and the sections that caused it.
type InvalidSectionError struct {
	Sections []string
	Err      error
}

func (e *InvalidSectionError) Error() string {
	return e.Err.Error() + ": " + strings.Join(e.Sections, ", ")
}

// IsInvalidSectionError returns a boolean indicating whether the error
// is reporting if a section of the definition is not a standard section
func IsInvalidSectionError(err error) bool {
	switch err.(type) {
	case *InvalidSectionError:
		return true
	}

	return false
}

// scanDefinitionFile is the SplitFunc for the scanner that will parse the deffile. It will split into tokens
// that designated by a line starting with %
//
// Scanner behavior:
//  1. The *first* time `s.Text()` is non-nil (which can be after infinitely many calls to
//     `s.Scan()`), that text is *guaranteed* to be the header, unless the header doesn't exist.
//     In that case it returns the first section it finds.
//  2. The next `n` times that `s.Text()` is non-nil (again, each could take many calls to
//     `s.Scan()`), that text is guaranteed to be one specific section of the definition file.
//  3. Once the input buffer is completely scanned, `s.Text()` will either be nil or non-nil
//     (in which case `s.Text()` contains the last section found of the input buffer) *and*
//     `s.Err()` will be non-nil with an `bufio.ErrFinalToken` returned. This is where scanning can completely halt.
func scanDefinitionFile(data []byte, atEOF bool) (advance int, token []byte, err error) {
	inSection := false
	var retbuf bytes.Buffer
	advance = 0

	l := len(data)

	for advance < l {
		// We are essentially a pretty wrapper to bufio.ScanLines.
		a, line, err := bufio.ScanLines(data[advance:], atEOF)
		if err != nil && err != bufio.ErrFinalToken {
			return 0, nil, err
		} else if line == nil { // If ScanLines returns a nil line, it needs more data. Send req for more data
			return 0, nil, nil // Returning 0, nil, nil requests Scanner.Scan() method find more data or EOF
		}

		_, word, err := bufio.ScanWords(line, true) // Tokenize the line into words
		if err != nil && err != bufio.ErrFinalToken {
			return 0, nil, err
		}

		// Check if the first word starts with % sign
		if word != nil && word[0] == '%' {
			// If the word starts with %, it's a section identifier

			// We no longer check if the word is a valid section identifier here, since we want to move to
			// a more modular approach where we can parse arbitrary sections
			if inSection {
				// Here we found the end of the section
				return advance, retbuf.Bytes(), nil
			} else if advance == 0 {
				// When advance == 0 and we found a section identifier, that means we have already
				// parsed the header out and left the % as the first character in the data. This means
				// we can now parse into sections.
				retbuf.Write(line)
				retbuf.WriteString("\n")
				inSection = true
			} else {
				// When advance != 0, that means we found the start of a section but there is
				// data before it. We return the data up to the first % and that is the header
				return advance, retbuf.Bytes(), nil
			}
		} else {
			// This line is not a section identifier
			retbuf.Write(line)
			retbuf.WriteString("\n")
		}

		// Shift the advance retval to the next line
		advance += a
		if a == 0 {
			break
		}
	}

	if !atEOF {
		return 0, nil, nil
	}

	return advance, retbuf.Bytes(), nil
}

func getSectionName(line string) string {
	// trim % prefix on section name
	line = strings.TrimLeft(line, "%")
	lineSplit := strings.SplitN(strings.ToLower(line), " ", 2)

	return lineSplit[0]
}

// parseTokenSection into appropriate components to be placed into a types.Script struct
func parseTokenSection(tok string, sections map[string]*types.Script, files *[]types.Files, appOrder *[]string) error {
	split := strings.SplitN(tok, "\n", 2)
	if len(split) != 2 {
		return fmt.Errorf("section %v: could not be split into section name and body", split[0])
	}

	key := getSectionName(split[0])

	// parse files differently to allow multiple files sections
	if key == "files" {
		f := types.Files{}
		sectionSplit := strings.SplitN(strings.TrimLeft(split[0], "%"), " ", 2)
		if len(sectionSplit) == 2 {
			f.Args = sectionSplit[1]
		}

		// Files are parsed as a map[string]string
		filesSections := strings.TrimSpace(split[1])
		subs := strings.Split(filesSections, "\n")
		for _, line := range subs {
			if line = strings.TrimSpace(line); line == "" || strings.Index(line, "#") == 0 {
				continue
			}
			var src, dst string
			// Split at space, but not within double quotes
			lineSubs := fileSplitter.FindAllString(line, -1)
			if len(lineSubs) < 2 {
				src = strings.TrimSpace(lineSubs[0])
				dst = ""
			} else {
				src = strings.TrimSpace(lineSubs[0])
				dst = strings.TrimSpace(lineSubs[1])
			}
			src = strings.Trim(src, "\"")
			dst = strings.Trim(dst, "\"")
			f.Files = append(f.Files, types.FileTransport{Src: src, Dst: dst})
		}

		// look through existing files and append to them if they already exist
		for i, ef := range *files {
			if ef.Args == f.Args {
				ef.Files = append(ef.Files, f.Files...)
				// replace old file struct with newly appended one
				(*files)[i] = ef
				return nil
			}
		}

		*files = append(*files, f)
		return nil
	}

	if appSections[key] {
		sectionSplit := strings.SplitN(strings.TrimLeft(split[0], "%"), " ", 3)
		if len(sectionSplit) < 2 {
			return fmt.Errorf("app section %v: could not be split into section name and app name", sectionSplit[0])
		}

		key = strings.Join(sectionSplit[0:2], " ")
		// create app script pbject to populate
		if _, ok := sections[key]; !ok {
			sections[key] = &types.Script{}
		}
		// Record the order in which we came across each app... since we have
		// to process their appinstall sections in that order.
		appName := sectionSplit[1]
		appKnown := false
		for _, a := range *appOrder {
			if a == appName {
				appKnown = true
				break
			}
		}
		if !appKnown {
			*appOrder = append(*appOrder, appName)
		}

	} else {
		// create section script object if its a non-standard section
		if _, ok := sections[key]; !ok {
			sections[key] = &types.Script{}
		}
		sectionSplit := strings.SplitN(strings.TrimLeft(split[0], "%"), " ", 2)
		if len(sectionSplit) == 2 {
			sections[key].Args = sectionSplit[1]
		}
	}

	sections[key].Script += split[1]
	return nil
}

func doSections(s *bufio.Scanner, d *types.Definition) error {
	sectionsMap := make(map[string]*types.Script)
	files := []types.Files{}
	appOrder := []string{}
	tok := strings.TrimSpace(s.Text())

	// skip initial token parsing if it is empty after trimming whitespace
	if tok != "" {
		// check if first thing parsed is a header/comment or just a section
		if tok[0] != '%' {
			if err := doHeader(tok, d); err != nil {
				return fmt.Errorf("failed to parse deffile header: %v", err)
			}
		} else {
			// this is a section
			if err := parseTokenSection(tok, sectionsMap, &files, &appOrder); err != nil {
				return err
			}
		}
	}

	// parse remaining sections while scanner can advance
	for s.Scan() {
		if err := s.Err(); err != nil {
			return err
		}

		tok := s.Text()

		// Parse each token -> section
		if err := parseTokenSection(tok, sectionsMap, &files, &appOrder); err != nil {
			return err
		}
	}

	if err := s.Err(); err != nil {
		return err
	}

	return populateDefinition(sectionsMap, &files, &appOrder, d)
}

func GetLabels(content string) map[string]string {
	// labels are parsed as a map[string]string
	labelsSections := strings.TrimSpace(content)
	subs := strings.Split(labelsSections, "\n")
	labels := make(map[string]string)

	for _, line := range subs {
		if line = strings.TrimSpace(line); line == "" || strings.Index(line, "#") == 0 {
			continue
		}
		var key, val string
		lineSubs := strings.SplitN(line, " ", 2)
		if len(lineSubs) < 2 {
			key = strings.TrimSpace(lineSubs[0])
			val = ""
		} else {
			key = strings.TrimSpace(lineSubs[0])
			val = strings.TrimSpace(lineSubs[1])
		}

		labels[key] = val
	}

	return labels
}

func populateDefinition(sections map[string]*types.Script, files *[]types.Files, appOrder *[]string, d *types.Definition) (err error) {
	// initialize standard sections if not already created
	// this function relies on standard sections being initialized in the map
	for section := range validSections {
		if _, ok := sections[section]; !ok {
			sections[section] = &types.Script{}
		}
	}

	d.ImageData = types.ImageData{
		ImageScripts: types.ImageScripts{
			Help:        *sections["help"],
			Environment: *sections["environment"],
			Runscript:   *sections["runscript"],
			Test:        *sections["test"],
			Startscript: *sections["startscript"],
		},
		Labels: GetLabels(sections["labels"].Script),
	}
	d.BuildData.Files = *files
	d.BuildData.Scripts = types.Scripts{
		Arguments: *sections["arguments"],
		Pre:       *sections["pre"],
		Setup:     *sections["setup"],
		Post:      *sections["post"],
		Test:      *sections["test"],
	}

	// remove standard sections from map
	for s := range validSections {
		delete(sections, s)
	}

	// add remaining sections to CustomData and throw error for invalid section(s)
	if len(sections) != 0 {
		// take remaining sections and store them as custom data
		d.CustomData = make(map[string]string)
		for k := range sections {
			d.CustomData[k] = sections[k].Script
		}
		var keys []string
		for k := range sections {
			sectionName := strings.Split(k, " ")
			if !appSections[sectionName[0]] {
				keys = append(keys, k)
			}
		}
		if len(keys) > 0 {
			return &InvalidSectionError{keys, errInvalidSection}
		}
	}

	// record order of any SCIF apps
	d.AppOrder = *appOrder

	// return error if no useful information was parsed into the struct
	if isEmpty(*d) {
		return errEmptyDefinition
	}

	return err
}

func doHeader(h string, d *types.Definition) error {
	h = strings.TrimSpace(h)
	toks := strings.Split(h, "\n")
	header := make(map[string]string)
	keyCont, valCont := "", ""

	for _, line := range toks {
		var key, val string
		// skip empty or comment lines
		if line = strings.TrimSpace(line); line == "" || strings.Index(line, "#") == 0 {
			if len(keyCont) > 0 {
				d.Header[keyCont] = valCont
				keyCont, valCont = "", ""
			}
			continue
		}

		// trim any comments on header lines
		trimLine := strings.Split(line, "#")[0]
		if len(valCont) == 0 {
			linetoks := strings.SplitN(trimLine, ":", 2)
			if len(linetoks) == 1 {
				return fmt.Errorf("header key %s had no val", linetoks[0])
			}

			key, val = strings.ToLower(strings.TrimSpace(linetoks[0])), strings.TrimSpace(linetoks[1])
		} else {
			key, val = keyCont, valCont+strings.TrimSpace(trimLine)
			keyCont, valCont = "", ""
		}
		// continuation
		if strings.HasSuffix(val, "\\") {
			keyCont = key
			valCont = strings.TrimSuffix(val, "\\")
			if strings.HasSuffix(valCont, "\\n") {
				valCont = strings.TrimSuffix(valCont, "\\n") + "\n"
			}
			continue
		}
		if _, ok := validHeaders[key]; !ok {
			rgx := regexp.MustCompile(`\d+$`)
			tmpKey := rgx.ReplaceAllString(key, "&n")
			if ok = tmpKey != key; ok {
				_, ok = validHeaders[tmpKey]
			}
			if !ok {
				return fmt.Errorf("invalid header keyword found: %s", key)
			}
		}
		header[key] = val
	}

	// only set header if some values are found
	if len(header) != 0 {
		d.Header = header
	}

	return nil
}

// ParseDefinitionFile receives a reader from a definition file
// and parse it into a Definition struct or return error if
// the definition file has a bad section.
func ParseDefinitionFile(r io.Reader) (d types.Definition, err error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return d, fmt.Errorf("while attempting to read definition file: %v", err)
	}

	d.FullRaw = raw
	d.Raw = raw

	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Split(scanDefinitionFile)

	// advance scanner until it returns a useful token or errors
	//nolint:revive
	for s.Scan() && s.Text() == "" && s.Err() == nil {
	}

	if s.Err() != nil {
		log.Println(s.Err())
		return d, s.Err()
	} else if s.Text() == "" {
		return d, errEmptyDefinition
	}

	if err = doSections(s, &d); err != nil {
		return d, err
	}

	return
}

// All receives a reader from a definition file
// and parses it into a slice of Definition structs or returns error if
// an error is encounter while parsing
func All(r io.Reader) ([]types.Definition, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("while attempting to read definition file: %v", err)
	}

	// copy raw data for parsing
	buf := raw
	rgx := regexp.MustCompile(`(?mi)^bootstrap:`)
	i := rgx.FindAllIndex(buf, -1)

	splitBuf := [][]byte{}
	// split up buffer based on index of delimiter
	for len(i) > 0 {
		index := i[len(i)-1][0]
		splitBuf = append([][]byte{buf[index:]}, splitBuf...)
		i = i[:len(i)-1]
		buf = buf[:index]
	}

	// add anything remaining above first found Bootstrap
	// handles case of no header
	splitBuf = append([][]byte{buf[:]}, splitBuf...)

	if len(splitBuf) == 0 {
		return nil, errEmptyDefinition
	}

	stages := make([]types.Definition, 0, len(splitBuf))
	for _, stage := range splitBuf {
		if len(stage) == 0 {
			continue
		}

		d, err := ParseDefinitionFile(bytes.NewReader(stage))
		if err != nil {
			if err == errEmptyDefinition {
				continue
			}
			return nil, err
		}

		d.FullRaw = raw
		stages = append(stages, d)
	}

	if len(stages) == 0 {
		return nil, errors.New("no stages found in definition file")
	}

	return stages, nil
}

// IsValidDefinition returns whether or not the given file is a valid definition
func IsValidDefinition(source string) (valid bool, err error) {
	defFile, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer defFile.Close()

	if s, err := defFile.Stat(); err != nil {
		return false, fmt.Errorf("unable to stat file: %v", err)
	} else if s.IsDir() {
		return false, nil
	}

	_, err = ParseDefinitionFile(defFile)
	if err != nil {
		return false, err
	}

	return true, nil
}

// isEmpty returns a bool indicating whether the given definition contains no parsed information
// due to the unique initialization state of the empty definition for this check, it should only
// be used by populateDefinition()
func isEmpty(d types.Definition) bool {
	// clear raw data for comparison
	d.Raw = nil
	d.FullRaw = nil

	// initialize empty definition fully
	emptyDef := types.Definition{}
	emptyDef.Labels = make(map[string]string)
	emptyDef.BuildData.Files = make([]types.Files, 0)
	emptyDef.AppOrder = []string{}

	return reflect.DeepEqual(d, emptyDef)
}

// validSections just contains a list of all the valid sections a definition file
// could contain. If any others are found, an error will generate
var validSections = map[string]bool{
	"help":        true,
	"setup":       true,
	"files":       true,
	"labels":      true,
	"environment": true,
	"pre":         true,
	"post":        true,
	"runscript":   true,
	"test":        true,
	"startscript": true,
	"arguments":   true,
}

var appSections = map[string]bool{
	"appinstall": true,
	"applabels":  true,
	"appfiles":   true,
	"appenv":     true,
	"apptest":    true,
	"apphelp":    true,
	"apprun":     true,
	"appstart":   true,
}

// validHeaders just contains a list of all the valid headers a definition file
// could contain. If any others are found, an error will generate
var validHeaders = map[string]bool{
	"bootstrap":    true,
	"from":         true,
	"includecmd":   true,
	"mirrorurl":    true,
	"updateurl":    true,
	"osversion":    true,
	"include":      true,
	"library":      true,
	"registry":     true,
	"namespace":    true,
	"stage":        true,
	"product":      true,
	"user":         true,
	"regcode":      true,
	"productpgp":   true,
	"registerurl":  true,
	"modules":      true,
	"otherurl&n":   true,
	"fingerprints": true,
	"confurl":      true,
	"setopt":       true,
	"target":       true,
	"frontend":     true,
	"filename":     true,
	"buildargs":    true,
}
