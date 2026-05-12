// SPDX-FileCopyrightText : © 2026 Galvanized Logic Inc.
// SPDX-License-Identifier: MIT

package main

// apigen.go remaps parts the vulkan API to better align with go conventions.
// This affects the vulkan API structs and commands.

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// =============================================================================
// structs

// spec struct information captured in egen.go.
type Struct struct {
	Name    string   //
	Mems    []Member //
	Ret     string   // "" or "retonly"
	Doc     string   // doc link
	Comment string   // deprecated comment

	// go structs that are not byte compatible with C struct.
	RequiresConvert bool // requires explicit struct conversion
}
type Member struct {
	Name             string
	Value            string // struct type
	Array            string
	Type             string
	Ptr              string
	Comment          string // deprecated comment
	IsNullTerminated bool   // string
	IsString         bool   // variable size.
	IsFixedString    bool   // fixed byte size.
	IsStringSlice    bool   // array of strings.
	IsSliceField     bool
	IsSliceCounter   bool

	// command parameter specific.
	IsBindingAllocatedArray bool
	IsSingleReturn          bool
	IsSliceReturn           bool
	IsByteArray             bool

	// used to find buffer+count pairs to convert into slices.
	Len     string    // "" or reference to the related "Count" member.
	lenMems []*Member // refs to the related array members for count.
}

// track the structs. Set by egen:genType
var allVkStructs = map[string]*Struct{}

// isStructType returns true if the given name is a struct.
func isStructType(structName string) bool {
	_, ok := allVkStructs[structName]
	return ok
}

// track the unions. Set by egen:genType
var allVkUnions = map[string]*Struct{}

// isUnionType returns true if the given name is a struct.
func isUnionType(unionName string) bool {
	_, ok := allVkUnions[unionName]
	return ok
}

// writeUnion generates code for a single c-union struct.
func writeUnion(sinfo *Struct, out *strings.Builder) {
	sinfo.RequiresConvert = true
	goname := strings.Replace(sinfo.Name, "Vk", "", 1)
	fmt.Fprintf(out, "%s\n", sinfo.Doc)
	fmt.Fprintf(out, "type %s struct {\n", goname)
	for _, mem := range sinfo.Mems {
		gotype := strings.Replace(mem.Type, "Vk", "", 1)
		govar := uppercaseFirst(mem.Name)
		fmt.Fprintf(out, "    %s %s%s\n", govar, mem.Array, gotype)
		fmt.Fprintf(out, "    as%s bool\n", govar)
	}
	fmt.Fprintf(out, "}\n\n")
	for _, mem := range sinfo.Mems {
		gotype := strings.Replace(mem.Type, "Vk", "", 1)
		govar := uppercaseFirst(mem.Name)
		fmt.Fprintf(out, "func (u *%s) As%s(val %s%s){\n", goname, govar, mem.Array, gotype)
		fmt.Fprintf(out, "   u.%s = val\n", govar)
		for _, m2 := range sinfo.Mems {
			g2v := uppercaseFirst(m2.Name)
			g2as := "as" + g2v
			vbool := "false"
			if govar == g2v {
				vbool = "true"
			}
			fmt.Fprintf(out, "   u.%s = %s\n", g2as, vbool)
		}
		fmt.Fprintf(out, "}\n\n")
	}

	// -------------------------------------------------------------------------
	// generate the private go struct that matches the vulkan type.
	vkname := strings.Replace(sinfo.Name, "Vk", "vk", 1)
	vktype := sinfo.Mems[0].Type
	vktype = strings.Replace(vktype, "Vk", "vk", 1)
	fmt.Fprintf(out, "type %s[unsafe.Sizeof(%s%s{})]byte\n", vkname, sinfo.Mems[0].Array, vktype)

	// -------------------------------------------------------------------------
	// generate the method to convert the go type to the vulkan type
	fmt.Fprintf(out, "func (u *%s) vkStruct() *%s {\n", goname, vkname)
	fmt.Fprintf(out, "   switch {\n")
	for _, mem := range sinfo.Mems {
		govar := uppercaseFirst(mem.Name)
		fmt.Fprintf(out, "   case u.as%s:\n", govar)
		fmt.Fprintf(out, "   return (*%s)(unsafe.Pointer(&u.%s))\n", vkname, govar)
	}
	fmt.Fprintf(out, "   default:\n")
	fmt.Fprintf(out, "      return &%s{}\n", vkname)
	fmt.Fprintf(out, "   }\n")
	fmt.Fprintf(out, "}\n\n")
}

// writeStruct generates code for a single struct.
func writeStruct(sinfo *Struct, out *strings.Builder) {

	// -------------------------------------------------------------------------
	// generate the public go struct
	goname := strings.Replace(sinfo.Name, "Vk", "", 1)
	fmt.Fprintf(out, "%s\n", sinfo.Doc)
	fmt.Fprintf(out, "type %s struct {\n", goname)
	for _, mem := range sinfo.Mems {
		mname := uppercaseFirst(mem.Name)
		mtype := strings.Replace(mem.Type, "Vk", "", 1)
		switch {
		case mem.IsSliceCounter:
			// hide the slice counter fields in the public definitions.
			sinfo.RequiresConvert = true
		case mname == "SType":
			// hide the struct type fields in the public definitions.
		case mem.Array != "" && mem.IsFixedString:
			fmt.Fprintf(out, "    %s string\n", mname)
			sinfo.RequiresConvert = true
		case mem.Array != "":
			fmt.Fprintf(out, "    %s %s%s\n", mname, mem.Array, mtype)
		case mem.IsStringSlice:
			fmt.Fprintf(out, "    %s []string\n", mname)
		case mem.IsSliceField:
			fmt.Fprintf(out, "    %s []%s\n", mname, mtype)
		case mem.IsString:
			fmt.Fprintf(out, "    %s string\n", mname)
		case mem.Ptr != "" && mtype != "unsafe.Pointer" && mtype != "string":
			fmt.Fprintf(out, "    %s %s%s\n", mname, mem.Ptr, mtype)
		default:
			fmt.Fprintf(out, "    %s %s\n", mname, mtype)
		}
	}
	fmt.Fprintf(out, "}\n\n")

	// -------------------------------------------------------------------------
	// generate the private go struct that matches the vulkan type.
	vkname := strings.Replace(sinfo.Name, "Vk", "vk", 1)
	if sinfo.RequiresConvert {
		fmt.Fprintf(out, "type %s struct {\n", vkname)
		for _, mem := range sinfo.Mems {
			mtype := strings.Replace(mem.Type, "Vk", "", 1)
			if isStructType(mem.Type) {
				mtype = strings.Replace(mem.Type, "Vk", "vk", 1)
			}
			switch {
			case mem.IsStringSlice:
				fmt.Fprintf(out, "    %s **%s\n", mem.Name, mtype)
			case mem.Ptr != "" && mtype != "unsafe.Pointer":
				fmt.Fprintf(out, "    %s %s%s\n", mem.Name, mem.Ptr, mtype)
			case mem.Array != "":
				fmt.Fprintf(out, "    %s %s%s\n", mem.Name, mem.Array, mtype)
			case mtype == "bool":
				fmt.Fprintf(out, "    %s Bool32\n", mem.Name)
			default:
				fmt.Fprintf(out, "    %s %s\n", mem.Name, mtype)
			}
		}
		fmt.Fprintf(out, "}\n\n")
	} else {
		fmt.Fprintf(out, "type %s = %s\n\n", vkname, goname)
	}

	// -------------------------------------------------------------------------
	// generate the method to convert the vulkan struct to the go struct
	fmt.Fprintf(out, "func (s *%s) goStruct() *%s {\n", vkname, goname)
	if sinfo.RequiresConvert {
		fmt.Fprintf(out, "   rval := &%s{\n", goname)
		for _, mem := range sinfo.Mems {
			if f := convertFieldVkToGo(mem); f != "" {
				fmt.Fprintf(out, "   %s\n", f)
			}
		}
		fmt.Fprintf(out, "   }\n")
	} else {
		fmt.Fprintf(out, "  rval := (*%s)(s)\n", vkname)
	}
	fmt.Fprintf(out, "   return rval\n")
	fmt.Fprintf(out, "}\n\n")

	// -------------------------------------------------------------------------
	// generate the method to convert the go struct to the vulkan struct
	fmt.Fprintf(out, "func (s *%s) vkStruct() *%s {\n", goname, vkname)
	fmt.Fprintf(out, "   if s == nil {\n")
	fmt.Fprintf(out, "      return nil\n")
	fmt.Fprintf(out, "   }\n")
	if sinfo.RequiresConvert {

		// point the array to the first element of the slice.
		for _, mem := range sinfo.Mems {
			if mem.IsString {
				continue
			}
			if mem.IsSliceField && mem.Array == "" {
				govar := uppercaseFirst(mem.Name)
				memStruct, isStruct := allVkStructs[mem.Type]
				mtype := strings.Replace(mem.Type, "Vk", "", 1)
				if isStruct {
					mtype = strings.Replace(mem.Type, "Vk", "vk", 1)
				}
				slicePtr := "sp_" + govar
				fmt.Fprintf(out, "   var %s %s%s\n", slicePtr, mem.Ptr, mtype)
				fmt.Fprintf(out, "   if len(s.%s) > 0 {\n", govar)
				switch {
				case isStruct && memStruct.RequiresConvert:
					fmt.Fprintf(out, "       tmp := make([]%s, len(s.%s))\n", mtype, govar)
					fmt.Fprintf(out, "       for i, v := range s.%s {\n", govar)
					fmt.Fprintf(out, "          tmp[i] = *(v.vkStruct())\n")
					fmt.Fprintf(out, "       }\n")
					fmt.Fprintf(out, "       %s = &tmp[0]\n", slicePtr)
				case isStruct:
					fmt.Fprintf(out, "       %s = &s.%s[0]\n", slicePtr, govar)
				case mem.IsStringSlice:
					fmt.Fprintf(out, "       tmp := make([]*%s, len(s.%s))\n", mtype, govar)
					fmt.Fprintf(out, "       for i, v := range s.%s {\n", govar)
					fmt.Fprintf(out, "          tmp[i] = sysStringToBytes(v)\n")
					fmt.Fprintf(out, "       }\n")
					fmt.Fprintf(out, "       %s = &tmp[0]\n", slicePtr)
				default:
					fmt.Fprintf(out, "       %s = &s.%s[0]\n", slicePtr, govar)
				}
				fmt.Fprintf(out, "   }\n")
			}
		}

		// generate the simple mappings.
		fmt.Fprintf(out, "   rval := &%s{\n", vkname)
		for _, mem := range sinfo.Mems {
			if field := convertFieldGoToVk(mem); field != "" {
				fmt.Fprintf(out, "   %s\n", field)
			}
		}
		fmt.Fprintf(out, "   }\n")

		// update the count members from the slice.
		for _, mem := range sinfo.Mems {
			if len(mem.lenMems) > 1 {
				fmt.Fprintf(out, "   rval.%s = 0\n", mem.Name)
				for _, sliceMem := range mem.lenMems {
					sliceName := uppercaseFirst(sliceMem.Name)
					fmt.Fprintf(out, "   if uint32(len(s.%s)) > rval.%s {\n", sliceName, mem.Name)
					fmt.Fprintf(out, "      rval.%s = uint32(len(s.%s))\n", mem.Name, sliceName)
					fmt.Fprintf(out, "   }\n")
				}
			}
		}
	} else {
		fmt.Fprintf(out, "  rval := (*%s)(s)\n", goname)
	}
	fmt.Fprintf(out, "   return rval\n")
	fmt.Fprintf(out, "}\n\n")
}

// convertFieldGoToVk maps internal vulkan struct fields to public go struct fields.
func convertFieldGoToVk(mem Member) string {
	goname := uppercaseFirst(mem.Name)
	vkname := mem.Name
	isStruct := isStructType(mem.Type)
	vktype := strings.Replace(mem.Type, "Vk", "", 1)
	if isStruct {
		vktype = strings.Replace(mem.Type, "Vk", "vk", 1)
	}
	switch {
	case vkname == "sType":
		if mem.Value == "" {
			return "" // BaseInStructure doesn't have this.
		}
		enum := strings.Replace(mem.Value, "VK_", "", 1)
		return fmt.Sprintf("   %s: %s,", vkname, enum)
	case mem.Type == "bool":
		return fmt.Sprintf("   %s : vkBool32(s.%s),", vkname, goname)
	case mem.IsStringSlice:
		slicePtr := "sp_" + goname
		return fmt.Sprintf("   %s: %s,", vkname, slicePtr)
	case mem.IsString:
		return fmt.Sprintf("   %s: sysStringToBytes(s.%s),", vkname, goname)
	case mem.IsSliceCounter && len(mem.lenMems) == 1:
		sliceName := uppercaseFirst(mem.lenMems[0].Name)
		return fmt.Sprintf("   %s: uint32(len(s.%s)),", vkname, sliceName)
	case mem.IsSliceCounter && len(mem.lenMems) > 1:
		return "" // counters referenced by multiple slices are handled later.
	case mem.IsSliceField && vktype == "Result" && mem.Ptr == "*":
		slicePtr := "sp_" + goname
		return fmt.Sprintf("   %s: %s,", vkname, slicePtr)
	case vktype == "Result" && mem.Ptr == "*":
		return fmt.Sprintf("   %s: (%s%s)(s.%s),", vkname, mem.Ptr, vktype, goname)
	case mem.IsSliceCounter:
		return "" // skipped: count field is intialized later.
	case !isStruct && mem.Array != "" && mem.IsFixedString:
		return fmt.Sprintf("   // %s: fixed string array not handled", vkname)
	case mem.IsSliceField:
		slicePtr := "sp_" + goname
		return fmt.Sprintf("   %s: %s,", vkname, slicePtr)
	case isStruct && mem.Ptr == "*":
		if len(mem.lenMems) > 0 {
			slicePtr := "sp_" + goname
			return fmt.Sprintf("   %s: %s,", vkname, slicePtr)
		}
		return fmt.Sprintf("   %s: (s.%s.vkStruct()),", vkname, goname)
	case mem.Array != "":
		return fmt.Sprintf("   %s: (%s%s)(s.%s),", vkname, mem.Array, vktype, goname)
	case isStruct && mem.Ptr == "":
		return fmt.Sprintf("   %s: *(s.%s.vkStruct()),", vkname, goname)
	case strings.HasPrefix(mem.Ptr, "["):
		return fmt.Sprintf("   %s: (%s%s)(s.%s),", vkname, mem.Ptr, vktype, goname)
	case mem.Ptr == "*" && vktype != "byte" && vktype != "unsafe.Pointer":
		return fmt.Sprintf("   %s: (%s%s)(s.%s),", vkname, mem.Ptr, vktype, goname)
	}
	return fmt.Sprintf("   %s: (%s)(s.%s),", vkname, vktype, goname)
}

// convertFieldVkToGo maps public go struct fields to internal vulkan struct fields.
func convertFieldVkToGo(mem Member) string {
	goname := uppercaseFirst(mem.Name)
	gotype := strings.Replace(mem.Type, "Vk", "", 1)
	vkname := mem.Name
	isStruct := isStructType(mem.Type)
	isUnion := isUnionType(mem.Type)
	switch {
	case vkname == "sType":
		return "" // hidden in go.
	case mem.Type == "bool":
		return fmt.Sprintf("   %s : goBool32(s.%s),", goname, vkname)
	case isUnion:
		return fmt.Sprintf("   // %s: can't convert union member to go\n", vkname)
	case len(mem.lenMems) > 0:
		return "" // count variable is part of go slice.
	case mem.Ptr != "" && mem.Type != "unsafe.Pointer":
		return "   // ignoring returned pointer type " + goname
	case isStruct && mem.Ptr == "" && mem.Array == "":
		return fmt.Sprintf("   %s: *(s.%s.goStruct()),", goname, vkname)
	case !isStruct && mem.Array != "" && mem.IsFixedString:
		return fmt.Sprintf("   %s: nullTermBytesToString(s.%s[:]),", goname, vkname)
	case mem.Array != "":
		return fmt.Sprintf("   %s: (%s%s)(s.%s),", goname, mem.Array, gotype, vkname)
	}
	return fmt.Sprintf("   %s: (%s)(s.%s),", goname, gotype, vkname)
}

// uppercaseFirst letter of the given string, returning a new string.
func uppercaseFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// lowercaseFirst letter of the given string, returning a new string.
func lowercaseFirst(s string) string {
	if len(s) == 0 {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}

// =============================================================================
// commands

// spec command information captured in egen.go.
type Cmd struct {
	Name       string   // function name
	Mems       []Member // parameters
	NumReturns int      // set when writing return parameters.

	// often: void, VkResult
	Type    string // sometimes: PFN_vkVoidFunction, VkDeviceAddress, uint64_t
	Doc     string // doc link
	Comment string // deprecated comment

	// the command is called once to get the number of elements,
	// and then again to fetch the elements. Identified by having
	// a returned array with a corresponding pointer reference counter.
	IsDoubleCall bool
}

// writeCmd generates both the cgo and syscall code for a single command.
func writeCmd(cinfo *Cmd, isCgo bool, out *strings.Builder) {

	// cmdPre generates command calls for "double calls"
	cmdPre(cinfo, out, isCgo) // identical pre-call content

	// c-api call is either cgo or syscall.
	// double call commands are generated in cmdPre.
	if !cinfo.IsDoubleCall {
		if isCgo {
			cmdCgo(cinfo, out)
		} else {
			cmdSys(cinfo, out)
		}
	}
	cmdPost(cinfo, out)
}

// cmdPre is the pre-c-api-call code.
func cmdPre(cinfo *Cmd, out *strings.Builder, isCgo bool) {
	fmt.Fprintf(out, "var %s vkCommand\n", cinfo.Name)
	goname := strings.Replace(cinfo.Name, "vk", "", 1)
	fmt.Fprintf(out, "%s\n", cinfo.Doc)
	fmt.Fprintf(out, "func %s(", goname)

	// function parameters
	fcnt := 0
	for _, mem := range cinfo.Mems {
		if mem.IsSingleReturn || mem.IsBindingAllocatedArray || mem.IsSliceReturn {
			continue // return parameter
		}
		if mem.IsSliceCounter {
			continue // counter is part of slice.
		}
		govar := goParamName(mem)
		mtype := strings.Replace(mem.Type, "Vk", "", 1)
		ptr := mem.Ptr
		if mem.IsSliceField {
			ptr = "[]"
		}
		switch {
		case mem.IsString:
			mtype = "string"
			ptr = ""
		case mtype == "unsafe.Pointer" && mem.Ptr == "*":
			mtype = "byte"
		}
		if fcnt == 0 {
			fmt.Fprintf(out, "    %s %s%s", govar, ptr, mtype)
		} else {
			fmt.Fprintf(out, ",\n    %s %s%s", govar, ptr, mtype)
		}
		fcnt += 1
	}

	// function return parameters
	rets := []string{}
	fmt.Fprintf(out, "    ) (")
	for _, mem := range cinfo.Mems {
		if mem.IsSliceCounter {
			continue // counter is part of slice.
		}
		govar := goParamName(mem)
		mtype := strings.Replace(mem.Type, "Vk", "", 1)
		switch {
		case mem.IsBindingAllocatedArray || mem.IsSliceReturn:
			rets = append(rets, fmt.Sprintf("%s []%s", govar, mtype))
		case mem.IsSingleReturn && mem.Ptr == "**":
			rets = append(rets, fmt.Sprintf("%s *byte", govar))
		case mem.IsSingleReturn:
			rets = append(rets, fmt.Sprintf("%s %s", govar, mtype))
		}
	}
	switch {
	case cinfo.Type == "VkResult":
		rets = append(rets, fmt.Sprintf("r error"))
	case cinfo.Type == "PFN_vkVoidFunction":
		rets = append(rets, fmt.Sprintf("fn PFN_vkVoidFunction"))
	}
	for i, ret := range rets {
		fmt.Fprintf(out, "%s", ret)
		if i < len(rets)-1 {
			fmt.Fprintf(out, ", ")
		}
	}
	fmt.Fprintf(out, ") {\n")
	cinfo.NumReturns = len(rets)

	// paramater conversions.
	counters := []string{} // catch reused counters, ie: vkCmdBindVertexBuffers
	for _, mem := range cinfo.Mems {
		govar := goParamName(mem)
		gotype := strings.Replace(mem.Type, "Vk", "", 1)
		vktype := strings.Replace(mem.Type, "Vk", "vk", 1)
		sinfo, isStruct := allVkStructs[mem.Type]

		// massage the parameters - case statement order matters.
		fmt.Fprintf(out, "\n")
		switch {
		case gotype == "bool" && mem.IsSingleReturn:
			fmt.Fprintf(out, "   // binding-allocated bool return value populated by Vulkan\n")
			fmt.Fprintf(out, "   tmpbool := vkBool32(%s)\n", govar)
			fmt.Fprintf(out, "   %sBool := &tmpbool\n", mem.Name)
		case gotype == "bool":
			fmt.Fprintf(out, "%sBool := vkBool32(%s)\n", mem.Name, mem.Name)
		case mem.IsSliceField && cinfo.IsDoubleCall:
			if len(mem.lenMems) != 1 {
				slog.Error("expecting one slice in a double call", "cmd", cinfo.Name)
				continue
			}
			counter := mem.lenMems[0]
			counterName := goParamName(*counter)
			mtype := strings.Replace(mem.Type, "Vk", "", 1)
			if mtype == "unsafe.Pointer" && mem.Ptr == "*" {
				mtype = "byte"
			}
			fmt.Fprintf(out, "// a double-call array output\n")
			fmt.Fprintf(out, "var %s %s\n", counterName, counter.Type)
			fmt.Fprintf(out, "%s := &%s\n", counter.Name, counterName)
			if isStruct {
				fmt.Fprintf(out, "var %s *%s\n", mem.Name, vktype)
			} else {
				fmt.Fprintf(out, "var %s *%s\n", mem.Name, mtype)
			}

			// first c-api call.
			fmt.Fprintf(out, "\n// first c-api call to get counter\n")
			if isCgo {
				cmdCgo(cinfo, out)
			} else {
				cmdSys(cinfo, out)
			}

			// allocate array
			fmt.Fprintf(out, "\n// allocate the array for the second call\n")
			if isStruct {
				fmt.Fprintf(out, "arr_%s := make([]%s, %s)\n", govar, vktype, counterName)
				fmt.Fprintf(out, "%s = make([]%s, %s)\n", govar, gotype, counterName)
			} else {
				fmt.Fprintf(out, "arr_%s := make([]%s, %s)\n", govar, mtype, counterName)
				fmt.Fprintf(out, "%s = make([]%s, %s)\n", govar, mtype, counterName)
			}
			fmt.Fprintf(out, "%s = &arr_%s[0]\n", mem.Name, govar)

			// second c-api call.
			fmt.Fprintf(out, "\n// second c-api call to get array\n")
			if isCgo {
				cmdCgoCall(cinfo, out)
			} else {
				cmdSysCall(cinfo, out)
			}

			// convert returned array
			fmt.Fprintf(out, "\n// convert the returned array to the go slice\n")
			fmt.Fprintf(out, "for i := range arr_%s {\n", govar)
			if isStruct {
				fmt.Fprintf(out, "    %s[i] = *arr_%s[i].goStruct()\n", govar, govar)
			} else {
				fmt.Fprintf(out, "    %s[i] = arr_%s[i]\n", govar, govar)
			}
			fmt.Fprintf(out, "}\n")
		case mem.IsSliceField && !isStruct && mem.IsSliceReturn:
			fmt.Fprintf(out, "   // output array allocated by the binding\n")
			fmt.Fprintf(out, "   %s = make([]Pipeline, %s)\n", govar, mem.Len)
			fmt.Fprintf(out, "   %s := unsafe.Pointer(&%s[0])\n", mem.Name, govar)
		case mem.IsSliceField && !isStruct:
			fmt.Fprintf(out, "   // input slice of values that do not need translation\n")
			if !slices.Contains(counters, mem.Len) {
				fmt.Fprintf(out, "   %s := len(%s)\n", mem.Len, govar)
			}
			fmt.Fprintf(out, "   var %s unsafe.Pointer\n", mem.Name)
			fmt.Fprintf(out, "   if %s != nil {\n", govar)
			fmt.Fprintf(out, "       %s = unsafe.Pointer(&%s[0])\n", mem.Name, govar)
			fmt.Fprintf(out, "   }\n")
			counters = append(counters, mem.Len)
		case isStruct && mem.IsSliceField:
			fmt.Fprintf(out, "   // input slice of struct that requires translation\n")
			fmt.Fprintf(out, "   var %s unsafe.Pointer\n", mem.Name)
			if !slices.Contains(counters, mem.Len) {
				fmt.Fprintf(out, "   %s := len(%s)\n", mem.Len, govar)
			}
			fmt.Fprintf(out, "   if len(%s) > 0 {\n", govar)
			fmt.Fprintf(out, "      tmp := make([]%s, %s)\n", vktype, mem.Len)
			fmt.Fprintf(out, "      for i, v := range %s {\n", govar)
			fmt.Fprintf(out, "         tmp[i] = *(v.vkStruct())\n")
			fmt.Fprintf(out, "      }\n")
			fmt.Fprintf(out, "      %s = unsafe.Pointer(&tmp[0])\n", mem.Name)
			fmt.Fprintf(out, "   }\n")
			counters = append(counters, mem.Len)
		case mem.IsBindingAllocatedArray:
			fmt.Fprintf(out, "   // binding-allocated array populated by Vulkan\n")
			fmt.Fprintf(out, "   %s = make([]%s, %s)\n", govar, gotype, mem.Len)
			fmt.Fprintf(out, "   %s := &%s[0]\n", mem.Name, govar)
		case isStruct && mem.Ptr == "*" && mem.IsSingleReturn && mem.IsSliceCounter:
			fmt.Fprintf(out, "   // double-call array output\n")
			fmt.Fprintf(out, "   var %s %s\n", goname, gotype)
			fmt.Fprintf(out, "   var %s := &%s\n", mem.Name, goname)
			fmt.Fprintf(out, "   var %s := &%s\n", mem.Name, goname)
		case isStruct && mem.Ptr == "*" && mem.IsSingleReturn:
			fmt.Fprintf(out, "   // binding-allocated single return value populated by Vulkan, requires translation\n")
			fmt.Fprintf(out, "   var %s *%s = %s.vkStruct()\n", mem.Name, vktype, govar)
		case isStruct && mem.Ptr == "*" && sinfo.RequiresConvert:
			fmt.Fprintf(out, "   // struct requiring translation\n")
			fmt.Fprintf(out, "   var %s *%s\n", mem.Name, vktype)
			fmt.Fprintf(out, "   if %s != nil {\n", govar)
			fmt.Fprintf(out, "       %s = %s.vkStruct()\n", mem.Name, govar)
			fmt.Fprintf(out, "   }\n")
		case mem.IsSingleReturn && mem.IsSliceCounter:
			// handled as part of the double call slice above.
		case mem.Ptr == "*" && mem.IsSingleReturn:
			fmt.Fprintf(out, "   // binding-allocated single return value populated by Vulkan\n")
			fmt.Fprintf(out, "   %s := &%s\n", mem.Name, govar)
		case mem.IsSingleReturn:
			fmt.Fprintf(out, "   // binding-allocated single return value populated by Vulkan\n")
			fmt.Fprintf(out, "   %s := &%s\n", mem.Name, govar)
		case mem.IsString:
			fmt.Fprintf(out, "   // string parameter conversion\n")
			fmt.Fprintf(out, "   var %s *byte\n", mem.Name)
			fmt.Fprintf(out, "   if %s != \"\" {\n", govar)
			fmt.Fprintf(out, "       %s = sysStringToBytes(%s)\n", mem.Name, govar)
			fmt.Fprintf(out, "   }\n")
		case mem.Ptr == "*":
			fmt.Fprintf(out, "   // singular input, pass direct\n")
			fmt.Fprintf(out, "   var %s unsafe.Pointer\n", mem.Name)
			fmt.Fprintf(out, "   if %s != nil {\n", govar)
			fmt.Fprintf(out, "      %s = unsafe.Pointer(%s)\n", mem.Name, govar)
			fmt.Fprintf(out, "   }\n")
		}
	}
}

// goParamName creates public API names for command parameters.
func goParamName(mem Member) (goname string) {
	goname = mem.Name
	if mem.Ptr == "*" && strings.HasPrefix(mem.Name, "p") {
		goname = strings.Replace(mem.Name, "p", "", 1)
		goname = lowercaseFirst(goname)
	}
	if mem.Ptr == "**" && strings.HasPrefix(mem.Name, "pp") {
		goname = strings.Replace(mem.Name, "pp", "", 1)
		goname = lowercaseFirst(goname)
	}
	return goname
}

// cmdSys generates the syscall c-api call code.
func cmdSys(cinfo *Cmd, out *strings.Builder) {
	cmdSysBind(cinfo.Name, out)
	if cinfo.Type == "VkResult" {
		fmt.Fprintf(out, "   var rsys uintptr\n")
	}
	cmdSysCall(cinfo, out)
}
func cmdSysBind(vkname string, out *strings.Builder) {
	fmt.Fprintf(out, "   if %s == nil {\n", vkname)
	fmt.Fprintf(out, "      %s = dlHandle.NewProc(\"%s\")\n", vkname, vkname)
	fmt.Fprintf(out, "   }\n")
}
func cmdSysCall(cinfo *Cmd, out *strings.Builder) {
	vkname := cinfo.Name
	vktype := cinfo.Type
	switch {
	case vktype == "VkResult":
		fmt.Fprintf(out, "   rsys, _, _ = syscall.SyscallN(%s.Addr(),\n", vkname)
	default:
		fmt.Fprintf(out, "   syscall.SyscallN(%s.Addr(),\n", vkname)
	}
	for _, mem := range cinfo.Mems {
		isStruct := isStructType(mem.Type)
		switch {
		case mem.Type == "bool" && mem.IsSingleReturn:
			fmt.Fprintf(out, "    uintptr(unsafe.Pointer(%sBool)),\n", mem.Name)
		case mem.Type == "bool":
			fmt.Fprintf(out, "    uintptr(%sBool),\n", mem.Name)
		case mem.IsString:
			fmt.Fprintf(out, "    uintptr(unsafe.Pointer(%s)),\n", mem.Name)
		case isStruct && mem.Ptr == "*":
			fallthrough
		case mem.IsBindingAllocatedArray, mem.IsSingleReturn:
			fmt.Fprintf(out, "    uintptr(unsafe.Pointer(%s)),\n", mem.Name)
		case mem.IsSliceField && !isStruct:
			fmt.Fprintf(out, "    uintptr(unsafe.Pointer(%s)),\n", mem.Name)
		default:
			fmt.Fprintf(out, "    uintptr(%s),\n", mem.Name)
		}
	}
	fmt.Fprintf(out, "   )\n") // end c-api call
	if vktype == "VkResult" {
		fmt.Fprintf(out, "   r = Result(uintptr(rsys))\n")
	}
}

// cmdCgo generates the syscall c-api call code.
// The command is in two helper-calls so the parts can be reused by double calls.
func cmdCgo(cinfo *Cmd, out *strings.Builder) {
	cmdSysBind(cinfo.Name, out)
	if cinfo.Type == "VkResult" {
		fmt.Fprintf(out, "   var rsys C.uintptr_t\n")
	}
	cmdSysCall(cinfo, out)
}
func cmdCgoBind(vkname string, out *strings.Builder) {
	fmt.Fprintf(out, "   if %s == nil {\n", vkname)
	fmt.Fprintf(out, "      %s = C.SymbolFromName(dlHandle, unsafe.Pointer(sysStringToBytes(\"%s\")))\n", vkname, vkname)
	fmt.Fprintf(out, "   }\n")
}
func cmdCgoCall(cinfo *Cmd, out *strings.Builder) {
	vkname := cinfo.Name
	vktype := cinfo.Type
	// pcnt is 3, 6, 9, 12, 15 based on the number of parameters.
	memCount, pcnt := len(cinfo.Mems), 0
	for i := memCount; i > 0; {
		i, pcnt = i-3, pcnt+3
	}

	// c-api call
	switch {
	case vktype == "VkResult":
		fmt.Fprintf(out, "   sys := C.Trampoline%d(%s, \n", pcnt, vkname)
	default:
		fmt.Fprintf(out, "   C.Trampoline%d(%s, \n", pcnt, vkname)
	}
	for _, mem := range cinfo.Mems {
		isStruct := isStructType(mem.Type)
		switch {
		case mem.Type == "bool" && mem.IsSingleReturn:
			fmt.Fprintf(out, "    uintptr(unsafe.Pointer(%sBool)),\n", mem.Name)
		case mem.Type == "bool":
			fmt.Fprintf(out, "    C.uintptr_t(uintptr(%sBool)),\n", mem.Name)
		case mem.IsString:
			fmt.Fprintf(out, "    C.uintptr_t(uintptr(unsafe.Pointer(%s))),\n", mem.Name)
		case isStruct && mem.Ptr == "*":
			fallthrough
		case mem.IsBindingAllocatedArray, mem.IsSingleReturn:
			fmt.Fprintf(out, "    C.uintptr_t(uintptr(unsafe.Pointer(%s))),\n", mem.Name)
		case mem.IsSliceField && !isStruct:
			fmt.Fprintf(out, "    C.uintptr_t(uintptr(unsafe.Pointer(%s))),\n", mem.Name)
		default:
			fmt.Fprintf(out, "    C.uintptr(uintptr(%s)),\n", mem.Name)
		}
	}
	for i := memCount; i < pcnt; i++ {
		fmt.Fprintf(out, "    0,\n") // unused trampoline parameters.
	}
	fmt.Fprintf(out, "   )\n") // end c-api call
	if vktype == "VkResult" {
		fmt.Fprintf(out, "   r = Result(rsys)\n")
	}
}

// cmdPost is the post-c-api-call code.
func cmdPost(cinfo *Cmd, out *strings.Builder) {
	for _, mem := range cinfo.Mems {
		govar := goParamName(mem)
		gotype := strings.Replace(mem.Type, "Vk", "", 1)
		isStruct := isStructType(mem.Type)
		switch {
		case gotype == "bool" && mem.IsSingleReturn:
			fmt.Fprintf(out, "%s = goBool32(tmpbool)\n", govar)
		case isStruct && mem.Ptr == "*" && mem.IsSingleReturn:
			fmt.Fprintf(out, "%s = *(%s.goStruct())\n", govar, mem.Name)
		}
	}
	if cinfo.Type == "VkResult" {
		fmt.Fprintf(out, "   if r == Result(0) {\n")
		fmt.Fprintf(out, "       r = SUCCESS\n")
		fmt.Fprintf(out, "   }\n")
	}
	if cinfo.NumReturns > 0 {
		fmt.Fprintf(out, "   return\n")
	}
	fmt.Fprintf(out, "}\n") // end function
}
