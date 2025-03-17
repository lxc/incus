// Copyright 2013 Google Inc.  All rights reserved.
// Copyright 2016 the gousb Authors.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package usbid

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// A Vendor contains the name of the vendor and mappings corresponding to all
// known products by their ID.
type Vendor struct {
	Name    string
	Product map[ID]*Product
}

// String returns the name of the vendor.
func (v Vendor) String() string {
	return v.Name
}

// A Product contains the name of the product (from a particular vendor) and
// the names of any interfaces that were specified.
type Product struct {
	Name      string
	Interface map[ID]string
}

// String returns the name of the product.
func (p Product) String() string {
	return p.Name
}

// A Class contains the name of the class and mappings for each subclass.
type Class struct {
	Name     string
	SubClass map[ClassCode]*SubClass
}

// String returns the name of the class.
func (c Class) String() string {
	return c.Name
}

// A SubClass contains the name of the subclass and any associated protocols.
type SubClass struct {
	Name     string
	Protocol map[Protocol]string
}

// String returns the name of the SubClass.
func (s SubClass) String() string {
	return s.Name
}

// ParseIDs parses and returns mappings from the given reader.  In general, this
// should not be necessary, as a set of mappings is already embedded in the library.
// If a new or specialized file is obtained, this can be used to retrieve the mappings,
// which can be stored in the global Vendors and Classes map.
func ParseIDs(r io.Reader) (map[ID]*Vendor, map[ClassCode]*Class, error) {
	vendors := make(map[ID]*Vendor, 2800)
	classes := make(map[ClassCode]*Class) // TODO(kevlar): count

	split := func(s string) (kind string, level int, id uint64, name string, err error) {
		pieces := strings.SplitN(s, "  ", 2)
		if len(pieces) != 2 {
			err = fmt.Errorf("malformatted line %q", s)
			return
		}

		// Save the name
		name = pieces[1]

		// Parse out the level
		for len(pieces[0]) > 0 && pieces[0][0] == '\t' {
			level, pieces[0] = level+1, pieces[0][1:]
		}

		// Parse the first piece to see if it has a kind
		first := strings.SplitN(pieces[0], " ", 2)
		if len(first) == 2 {
			kind, pieces[0] = first[0], first[1]
		}

		// Parse the ID
		i, err := strconv.ParseUint(pieces[0], 16, 16)
		if err != nil {
			err = fmt.Errorf("malformatted id %q: %w", pieces[0], err)
			return
		}

		id = i

		return
	}

	// Hold the interim values
	var vendor *Vendor
	var device *Product

	parseVendor := func(level int, raw uint64, name string) error {
		id := ID(raw)

		switch level {
		case 0:
			vendor = &Vendor{
				Name: name,
			}

			vendors[id] = vendor

		case 1:
			if vendor == nil {
				return fmt.Errorf("product line without vendor line")
			}

			device = &Product{
				Name: name,
			}

			if vendor.Product == nil {
				vendor.Product = make(map[ID]*Product)
			}

			vendor.Product[id] = device

		case 2:
			if device == nil {
				return fmt.Errorf("interface line without device line")
			}

			if device.Interface == nil {
				device.Interface = make(map[ID]string)
			}

			device.Interface[id] = name

		default:
			return fmt.Errorf("too many levels of nesting for vendor block")
		}

		return nil
	}

	// Hold the interim values
	var class *Class
	var subclass *SubClass

	parseClass := func(level int, id uint64, name string) error {
		switch level {
		case 0:
			class = &Class{
				Name: name,
			}

			classes[ClassCode(id)] = class

		case 1:
			if class == nil {
				return fmt.Errorf("subclass line without class line")
			}

			subclass = &SubClass{
				Name: name,
			}

			if class.SubClass == nil {
				class.SubClass = make(map[ClassCode]*SubClass)
			}

			class.SubClass[ClassCode(id)] = subclass

		case 2:
			if subclass == nil {
				return fmt.Errorf("protocol line without subclass line")
			}

			if subclass.Protocol == nil {
				subclass.Protocol = make(map[Protocol]string)
			}

			subclass.Protocol[Protocol(id)] = name

		default:
			return fmt.Errorf("too many levels of nesting for class")
		}

		return nil
	}

	var kind string

	lines := bufio.NewReaderSize(r, 512)
parseLines:
	for lineno := 0; ; lineno++ {
		b, isPrefix, err := lines.ReadLine()
		switch {
		case err == io.EOF:
			break parseLines
		case err != nil:
			return nil, nil, err
		case isPrefix:
			return nil, nil, fmt.Errorf("line %d: line too long", lineno)
		}

		line := string(b)

		if len(line) == 0 || line[0] == '#' {
			continue
		}

		k, level, id, name, err := split(line)
		if err != nil {
			return nil, nil, fmt.Errorf("line %d: %w", lineno, err)
		}

		if k != "" {
			kind = k
		}

		switch kind {
		case "":
			err = parseVendor(level, id, name)
		case "C":
			err = parseClass(level, id, name)
		}

		if err != nil {
			return nil, nil, fmt.Errorf("line %d: %w", lineno, err)
		}
	}

	return vendors, classes, nil
}
