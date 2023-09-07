package usbid

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

import (
	"fmt"
	"strconv"
)

// ID represents a vendor or product ID.
type ID uint16

// String returns a hexadecimal ID.
func (id ID) String() string {
	return fmt.Sprintf("%04x", int(id))
}

// ClassCode represents a USB-IF (Implementers Forum) class or subclass code.
type ClassCode uint8

// Standard classes defined by USB spec, see https://www.usb.org/defined-class-codes
const (
	ClassPerInterface       ClassCode = 0x00
	ClassAudio              ClassCode = 0x01
	ClassComm               ClassCode = 0x02
	ClassHID                ClassCode = 0x03
	ClassPhysical           ClassCode = 0x05
	ClassImage              ClassCode = 0x06
	ClassPTP                ClassCode = ClassImage // legacy name for image
	ClassPrinter            ClassCode = 0x07
	ClassMassStorage        ClassCode = 0x08
	ClassHub                ClassCode = 0x09
	ClassData               ClassCode = 0x0a
	ClassSmartCard          ClassCode = 0x0b
	ClassContentSecurity    ClassCode = 0x0d
	ClassVideo              ClassCode = 0x0e
	ClassPersonalHealthcare ClassCode = 0x0f
	ClassAudioVideo         ClassCode = 0x10
	ClassBillboard          ClassCode = 0x11
	ClassUSBTypeCBridge     ClassCode = 0x12
	ClassDiagnosticDevice   ClassCode = 0xdc
	ClassWireless           ClassCode = 0xe0
	ClassMiscellaneous      ClassCode = 0xef
	ClassApplication        ClassCode = 0xfe
	ClassVendorSpec         ClassCode = 0xff
)

var classDescription = map[ClassCode]string{
	ClassPerInterface:       "per-interface",
	ClassAudio:              "audio",
	ClassComm:               "communications",
	ClassHID:                "human interface device",
	ClassPhysical:           "physical",
	ClassImage:              "image",
	ClassPrinter:            "printer",
	ClassMassStorage:        "mass storage",
	ClassHub:                "hub",
	ClassData:               "data",
	ClassSmartCard:          "smart card",
	ClassContentSecurity:    "content security",
	ClassVideo:              "video",
	ClassPersonalHealthcare: "personal healthcare",
	ClassAudioVideo:         "audio/video",
	ClassBillboard:          "billboard",
	ClassUSBTypeCBridge:     "USB type-C bridge",
	ClassDiagnosticDevice:   "diagnostic device",
	ClassWireless:           "wireless",
	ClassMiscellaneous:      "miscellaneous",
	ClassApplication:        "application-specific",
	ClassVendorSpec:         "vendor-specific",
}

func (c ClassCode) String() string {
	d, ok := classDescription[c]
	if ok {
		return d
	}

	return strconv.Itoa(int(c))
}

// Protocol is the interface class protocol, qualified by the values
// of interface class and subclass.
type Protocol uint8

func (p Protocol) String() string {
	return strconv.Itoa(int(p))
}
