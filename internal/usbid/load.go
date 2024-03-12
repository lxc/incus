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
	"log"
	"os"
)

var (
	// Vendors stores the vendor and product ID mappings.
	Vendors map[ID]*Vendor

	// Classes stores the class, subclass and protocol mappings.
	Classes map[ClassCode]*Class
)

// Load reads the USB database from disk.
func Load() {
	usbidFile := os.Getenv("INCUS_USBIDS_PATH")
	if usbidFile == "" {
		usbidFile = "/usr/share/misc/usb.ids"
	}

	usbids, err := os.Open(usbidFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("usbid: failed to load: %s", err)
		}

		return
	}

	defer func() { _ = usbids.Close() }()

	ids, cls, err := ParseIDs(usbids)
	if err != nil {
		log.Printf("usbid: failed to parse: %s", err)
		return
	}

	Vendors = ids
	Classes = cls
}
