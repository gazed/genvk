// SPDX-FileCopyrightText : © 2026 Galvanized Logic Inc.
// SPDX-License-Identifier: MIT

package main

// ereg.go is a vulkan vk.xml specification parser based on a
// (very rough, partial) golang port of the official pythyon spec
// parser scripts. See: https://github.com/KhronosGroup/Vulkan-Docs
// - scripts/reg.py
//
// The layout of this file roughly follows the layout of the reg.py file.

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	// golang xml parsing similar to python etree.
	"github.com/beevik/etree" // godoc: https://pkg.go.dev/github.com/beevik/etree
)

// =============================================================================
// helper functions

// apiNameMatch :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Return whether a required api name matches a pattern specified for an
// XML <feature> 'api' attribute or <extension> 'supported' attribute.
func apiNameMatch(str string, supported string) bool {
	if str != "" {
		return supported == "" || slices.Contains(strings.Split(supported, ","), str)
	}
	return false // Fallthrough case - either str is None or the test failed
}

// matchAPIProfile :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Return whether an API and profile
// being generated matches an element's profile
func matchAPIProfile(api, profile string, elem *etree.Element) bool {

	// Match 'api', if present
	elemApi := elem.SelectAttrValue("api", "")
	if elemApi != "" {
		if api == "" {
			slog.Warn("No API requested, but api attribute is present", "api", elemApi)
		} else if api != elemApi {
			return false // Requested API does not match attribute
		}
	}
	elemProfile := elem.SelectAttrValue("profile", "")
	if elemProfile != "" {
		if profile == "" {
			slog.Warn("No profile requested, but profile attribute is present", "profile", elemProfile)
		} else if profile != elemProfile {
			return false // Requested profile does not match attribute
		}
	}
	return true
}

// FUTURE add mergeAPIs if needed ----------------------------------------------
// -----------------------------------------------------------------------------

// mergeInternalFeatures :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Merge internal API features (apitype='internal') into their public dependents.
// based on reg.py:mergeInternalFeatures
//
// This processes the tree to find features marked with apitype='internal' and merges
// their <require>, <deprecate>, and <remove> blocks into the first public feature
// that depends on them. After merging, the internal features are removed from the tree.
func mergeInternalFeatures(tree *etree.Element, apiName string) {

	// Find all features in the tree
	features := tree.FindElements("//feature")

	// Separate internal and public features
	internalFeatures := []*etree.Element{}
	publicFeatures := []*etree.Element{}
	for _, feature := range features {
		api := feature.SelectAttr("api")
		apiType := feature.SelectAttr("apitype")

		// Only process features matching the target API
		if api == nil {
			continue
		}
		if !slices.Contains(strings.Split(api.Value, ","), apiName) {
			continue
		}
		if apiType != nil && apiType.Value == "internal" {
			internalFeatures = append(internalFeatures, feature)
		} else {
			publicFeatures = append(publicFeatures, feature)
		}
	}
	slog.Info("Found features", "internal", len(internalFeatures), "public", len(publicFeatures))

	// Create a map of all features for dependency lookups
	allFeaturesMap := map[string]*etree.Element{}
	for _, f := range publicFeatures {
		featName := f.SelectAttr("name")
		allFeaturesMap[featName.Value] = f
	}
	for _, f := range internalFeatures {
		featName := f.SelectAttr("name")
		allFeaturesMap[featName.Value] = f
	}

	//	Check if feature depends on target_name (directly or transitively).
	var hasDependency func(feature *etree.Element, targetName string, allFeaturesMap map[string]*etree.Element, visited []string) bool
	hasDependency = func(feature *etree.Element, targetName string, allFeaturesMap map[string]*etree.Element, visited []string) bool {
		featureName := feature.SelectAttr("name")
		if featureName == nil || slices.Contains(visited, featureName.Value) {
			return false
		}
		visited = append(visited, featureName.Value)
		deps := getDependencies(feature)
		if slices.Contains(deps, targetName) {
			return true
		}

		// Check transitive dependencies
		for _, depName := range deps {
			if f, ok := allFeaturesMap[depName]; ok {
				if hasDependency(f, targetName, allFeaturesMap, visited) {
					return true
				}
			}
		}
		return false
	}

	// For each internal feature, find its first public dependent and merge
	for _, internalFeature := range internalFeatures {
		internalName := internalFeature.SelectAttr("name")
		slog.Debug("merging internal feature", "feature", internalName.Value)

		// Find the first public feature that depends on this internal feature
		var targetFeature *etree.Element
		for _, publicFeature := range publicFeatures {
			if hasDependency(publicFeature, internalName.Value, allFeaturesMap, []string{}) {
				targetFeature = publicFeature
				break
			}
		}

		// Merge internal features into public features.
		if targetFeature != nil {

			// Merge require blocks
			for _, require := range internalFeature.FindElements("require") {
				targetFeature.AddChild(require.Copy())
			}

			// Merge deprecate blocks
			for _, deprecate := range internalFeature.FindElements("deprecate") {
				targetFeature.AddChild(deprecate.Copy())
			}

			// Merge remove blocks
			for _, remove := range internalFeature.FindElements("remove") {
				targetFeature.AddChild(remove.Copy())
			}

			// Remove the internal feature from the tree
			internalFeature.Parent().RemoveChild(internalFeature)
		}
	}
}

// Build a simple dependency map from a features 'depends' attributes
// Extract all dependencies from a feature's depends attribute.
func getDependencies(feature *etree.Element) (deps []string) {
	depends := feature.SelectAttrValue("depends", "")
	if depends == "" {
		return deps
	}

	// Parse the depends expression - for simplicity, extract feature names
	// Dependencies can be like "VK_VERSION_1_0" or "VK_VERSION_1_0+VK_KHR_feature"
	// Split on + and , to get individual dependencies
	dv := strings.ReplaceAll(depends, "+", ",")
	for _, dep := range strings.Split(dv, ",") {
		dep = strings.TrimSpace(dep)
		dep = strings.ReplaceAll(dep, "(", "")
		dep = strings.ReplaceAll(dep, ")", "")
		if dep != "" {
			deps = append(deps, dep)
		}
	}
	return deps
}

// stripNonmatchingAPIs :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Remove tree Elements where 'api' attributes do not match apiName.
// based on reg.py:stripNonmatchingAPIs
func stripNonmatchingAPIs(root *etree.Element, apiName string) { // (tree, apiName, actuallyDelete = True):
	for _, child := range root.FindElements("//*[@api]") {
		api := child.SelectAttr("api")
		if apiNameMatch(apiName, api.Value) {
			// leave node in tree
		} else {
			// slog.Debug("removing api", "node", child.FullTag(), "api", api)
			child.Parent().RemoveChild(child)
		}
	}
}

// =============================================================================
// data structs based on the reg.py:python classes.

// Info is any of the registry feature data types derived from BaseInfo.
type Info interface {
	compareElem(info Info, infoName string) bool
	element() *etree.Element
	isRequired() bool
	isDeclared() bool
	setDeclared(declared bool)
}

// BaseInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Base class for information about a registry feature
// (type/group/enum/command/API/extension).
// Represents the state of a registry feature, used during API generation.
type BaseInfo struct {
	// should this feature be defined during header generation
	// (has it been removed by a profile or version)?
	required bool

	declared bool           // has this feature been defined already?
	elem     *etree.Element // etree Element for this feature

	deprecatedbyversion    string
	supersededby           string
	deprecatedbyextensions []string
	deprecatedlink         string
	vendor                 string
}

func NewBaseInfo(elem *etree.Element) *BaseInfo {
	return &BaseInfo{elem: elem}
}

// Reset required/declared to initial values. Used
// prior to generating a new API interface.
func (b *BaseInfo) resetState() {
	b.required = false
	b.declared = false
}

func (b *BaseInfo) element() *etree.Element { return b.elem }
func (b *BaseInfo) isRequired() bool        { return b.required }
func (b *BaseInfo) isDeclared() bool        { return b.declared }
func (b *BaseInfo) setDeclared(d bool)      { b.declared = d }

// Return True if self.elem and info.elem have the same attribute
// value for key.
// If 'required' is not True, also returns True if neither element
// has an attribute value for key.
func (b *BaseInfo) compareKeys(info Info, key string, required bool) bool {
	baseAttr := b.elem.SelectAttr(key)
	if required && baseAttr == nil {
		return false
	}

	infoAttr := info.element().SelectAttr(key)
	if baseAttr == nil || infoAttr == nil {
		return false
	}
	return baseAttr.Value == infoAttr.Value
}

// Return True if self.elem and info.elem have the same definition.
// info - the other object
// infoName - 'type' / 'group' / 'enum' / 'command' / 'feature' / 'extension'
func (b *BaseInfo) compareElem(info Info, infoName string) bool {
	if infoName == "enum" {
		if b.compareKeys(info, "extends", false) {

			// Either both extend the same type, or no type
			if b.compareKeys(info, "value", true) ||
				b.compareKeys(info, "bitpos", true) {
				return true // If both specify the same value or bit position, they are equal
			} else if b.compareKeys(info, "extnumber", false) &&
				b.compareKeys(info, "offset", false) &&
				b.compareKeys(info, "dir", false) {
				return true // If both specify the same relative offset, they are equal
			} else if b.compareKeys(info, "alias", false) {
				return true // both are aliases of the same value
			}
			return false
		} else {
			return false // The same enum cannot extend two different types
		}
	}
	return false // Non-<enum>s should never be redefined
}

// TypeInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
type TypeInfo struct {
	BaseInfo
	additionalValidity []*etree.Element
	removedValidity    []*etree.Element
}

func NewTypeInfo(elem *etree.Element) *TypeInfo {
	return &TypeInfo{BaseInfo: BaseInfo{elem: elem}}
}

// Get a collection of all member elements for this type, if any.
func (t *TypeInfo) getMembers() []*etree.Element {
	return t.BaseInfo.elem.FindElements("member")
}

func (t *TypeInfo) resetState() {
	t.BaseInfo.resetState()
	t.additionalValidity = []*etree.Element{}
	t.removedValidity = []*etree.Element{}
}

// GroupInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
type GroupInfo struct {
	BaseInfo
}

func NewGroupInfo(elem *etree.Element) *GroupInfo {
	return &GroupInfo{BaseInfo: BaseInfo{elem: elem}}
}

// EnumInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
type EnumInfo struct {
	BaseInfo
	etype string
}

func NewEnumInfo(elem *etree.Element) *EnumInfo {
	ei := &EnumInfo{BaseInfo: BaseInfo{elem: elem}}

	// numeric type of the value of the <enum> tag
	// ( '' for GLint, 'u' for GLuint, 'ull' for GLuint64 )
	ei.etype = elem.SelectAttrValue("type", "")
	return ei
}

// CmdInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
type CmdInfo struct {
	BaseInfo
	additionalValidity []*etree.Element
	removedValidity    []*etree.Element
}

func NewCmdInfo(elem *etree.Element) *CmdInfo {
	return &CmdInfo{BaseInfo: BaseInfo{elem: elem}}
}

// Get a collection of all param elements for this command, if any.
func (c *CmdInfo) getParams() []*etree.Element {
	return c.elem.FindElements("param")
}

func (c *CmdInfo) resetState() {
	c.BaseInfo.resetState()
	c.additionalValidity = []*etree.Element{}
	c.removedValidity = []*etree.Element{}
}

// FeatureInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
type FeatureInfo struct {
	BaseInfo
	name string // feature name string (e.g. 'VK_KHR_surface')

	emit bool // has this feature been defined already?

	// explicit numeric sort key within feature and extension groups.
	// Defaults to 0.
	sortorder int //self.sortorder = int(elem.get('sortorder', 0))

	category string // category, e.g. VERSION or khr/vendor tag
	version  string // feature name string

	// attribute of <feature>. Extensions do not have API version
	versionNumber string           // versionNumber - API version number, taken from the 'number'
	number        int              // numbers and are assigned number 0.
	supported     string           //
	deprecates    []*etree.Element //
}

func NewFeatureInfo(elem *etree.Element) *FeatureInfo {
	fi := &FeatureInfo{BaseInfo: BaseInfo{elem: elem}}
	fi.name = elem.SelectAttrValue("name", "")

	// Determine element category (vendor). Only works
	// for <extension> elements.
	if fi.elem.Tag == "feature" {
		fi.category = "VERSION" // Element category (vendor) is meaningless for <feature>
		fi.version = elem.SelectAttrValue("name", "")
		fi.versionNumber = elem.SelectAttrValue("number", "")
		fi.number = 0
		fi.supported = ""
		fi.deprecates = elem.FindElements("deprecate")
	} else {
		// Extract vendor portion of <APIprefix>_<vendor>_<name>
		fi.category = "" // self.name.split('_', 2)[1]
		fi.version = "0"
		fi.versionNumber = "0"

		// extension number, used for ordering and for assigning
		// enumerant offsets. <feature> features do not have extension
		// numbers and are assigned number 0, as are extensions without
		// numbers, so sorting works.
		var err error
		numstr := elem.SelectAttrValue("number", "0")
		fi.number, err = strconv.Atoi(numstr)
		if err != nil {
			slog.Error("invalid feature number", "number", numstr)
		}
		fi.supported = elem.SelectAttrValue("supported", "disabled")
	}
	return fi
}

// FUTURE add other classes from reg.py as needed ------------------------------
// -----------------------------------------------------------------------------

// =============================================================================
// main registry parsing class.

// Registry :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
type Registry struct {
	api  string         // only "vulkan" has been tested.
	tree *etree.Element // tree Element containing the root `<registry>`

	// parsed data maps.
	typedict      map[string]Info   // dictionary of TypeInfo objects keyed by type name
	groupdict     map[string]Info   // dictionary of GroupInfo objects keyed by group name
	enumdict      map[string]Info   // dictionary of EnumInfo objects keyed by enum name
	cmddict       map[string]Info   // dictionary of CmdInfo objects keyed by command name
	aliasdict     map[string]string // dictionary of type and command names mapped to their alias, such as VkFooKHR -> VkFoo
	enumvaluedict map[string]string // dictionary of enum values mapped to their type, such as VK_FOO_VALUE -> VkFoo
	apidict       map[string]Info   // dictionary of FeatureInfo objects for `<feature>` elements keyed by feature name
	extensions    []*etree.Element  // list of `<extension>` Elements

	// FeatureInfo Dictionaries
	extdict map[string]Info // for `<extension>` elements keyed by extension name
	gen     *generator      // generator
}

// NewRegistry is called with tree parsed from the vulkan xml specification.
func NewRegistry(tree *etree.Document) *Registry {
	if tree == nil {
		slog.Error("invalid specfication.")
		return nil
	}
	root := tree.Root()
	if root == nil || root.Tag != "registry" {
		slog.Error("not a valid spec root", "tag", root.Tag)
		return nil
	}
	return &Registry{
		tree:          root,
		typedict:      map[string]Info{},
		groupdict:     map[string]Info{},
		enumdict:      map[string]Info{},
		cmddict:       map[string]Info{},
		aliasdict:     map[string]string{},
		enumvaluedict: map[string]string{},
		apidict:       map[string]Info{},
		extdict:       map[string]Info{},
	}
}

// addElementInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Add information about an element to the corresponding dictionary.
// Intended for internal use only.
func (reg *Registry) addElementInfo(elem *etree.Element, info Info, infoName string, dictionary map[string]Info) {
	key := elem.SelectAttrValue("name", "")
	if existing, ok := dictionary[key]; ok {
		if !existing.compareElem(info, infoName) {
			// reg.py code says this should not happen

			// HACK: use the one with an extension number.
			exnum := existing.element().SelectAttrValue("extnumber", "")
			innum := elem.SelectAttrValue("extnumber", "")
			if exnum == "" && innum != "" {
				slog.Info("redefining key", "key", key, "exnum", exnum, "innum", innum)
				dictionary[key] = info
			}
			return
		}
	} else {
		dictionary[key] = info
	}
}

// lookupElementInfo :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Find a {Type|Enum|Cmd}Info object by name.
func (reg *Registry) lookupElementInfo(fname string, dictionary map[string]Info) Info {
	if info, ok := dictionary[fname]; ok {
		return info // Found generic element for feature', fname
	}
	return nil
}

// addEnumValue :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Track aliasing and map back from enum values to their type
func (reg *Registry) addEnumValue(enum *etree.Element, typeName string) {

	// Record alias, if any
	value := enum.SelectAttrValue("name", "")
	alias := enum.SelectAttrValue("alias", "")
	if alias != "" {
		reg.aliasdict[value] = alias
	}

	// Map the value back to the type
	if _, ok := reg.aliasdict[typeName]; ok {
		typeName = reg.aliasdict[typeName]
	}

	// some times the same enum is defined by multiple extensions
	// Check that the names match
	if val, ok := reg.enumvaluedict[value]; ok {
		if typeName != val {
			// should not happen
			slog.Error("enum defined by multiple extensions", "enum", value)
		}
	} else {
		reg.enumvaluedict[value] = typeName
	}
}

// parseTree :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Parse the registry Element, once created
func (reg *Registry) parseTree(api string) {
	if reg.tree == nil {
		slog.Error("use NewRegistry with a valid specification tree document.")
		return
	}
	root := reg.tree

	// Preprocess the tree to or remove all elements with non-matching 'api' attributes
	stripNonmatchingAPIs(root, api) // get rid of the other APIs

	// Merge internal features (apitype="internal") into their public dependents
	// This happens after API merging/stripping so we work with the correct API
	mergeInternalFeatures(root, api)

	// Get vendor tags
	vendors := []string{}
	for _, tag := range root.FindElements("//tags/tag") {
		tagName := tag.SelectAttrValue("name", "")
		vendors = append(vendors, tagName)
	}
	slog.Info("found vendors", "count", len(vendors))

	// Function to check which (if any) vendor suffix is present on an API name
	getApiVendorTag := func(name string) string {
		for _, vendor := range vendors {
			if strings.HasSuffix(name, vendor) {
				return vendor
			}
		}
		return ""
	}

	// Create dictionary of registry types from toplevel <types> tags
	// and add 'name' attribute to each <type> tag (where missing)
	// based on its <name> element.
	//
	// There is usually one <types> block; more are OK
	// Required <type> attributes: 'name' or nested <name> tag contents
	//     self.typedict = {}
	for _, typeElem := range root.FindElements("types/type") {

		// If the <type> does not already have a 'name' attribute, set
		// it from contents of its <name> tag, or from the contents of
		// its <proto><name> tag for funcpointer types.
		name := typeElem.SelectAttrValue("name", "")
		if name == "" {
			var nameElem *etree.Element
			cat := typeElem.SelectAttr("category")
			if cat != nil && cat.Value == "funcpointer" {
				nameElem = typeElem.FindElement("proto/name")
			} else {
				nameElem = typeElem.FindElement("name")
			}
			if nameElem == nil && typeElem.Text() == "" {
				slog.Error("Type without a name", "type", fmt.Sprintf("%v", typeElem))
				return
			}
			nameStr := nameElem.Text()
			typeElem.CreateAttr("name", nameStr)
		}

		typeInfo := NewTypeInfo(typeElem)
		typeInfo.vendor = getApiVendorTag(name)
		reg.addElementInfo(typeElem, typeInfo, "type", reg.typedict)

		// Record alias, if any
		alias := typeElem.SelectAttrValue("alias", "")
		if alias != "" {
			reg.aliasdict[name] = alias
		}
	}
	slog.Info("added type info", "count", len(reg.typedict))

	// Create dictionary of registry enum groups from <enums> tags.
	//
	// Required <enums> attributes: 'name'. If no name is given, one is
	// generated, but that group cannot be identified and turned into an
	// enum type definition - it is just a container for <enum> tags.
	for _, group := range root.FindElements("enums") {
		groupInfo := NewGroupInfo(group)
		reg.addElementInfo(group, groupInfo, "group", reg.groupdict)
	}
	slog.Info("added enum group info", "count", len(reg.groupdict))

	// Create dictionary of registry enums from <enum> tags
	//
	// <enums> tags usually define different namespaces for the values
	//   defined in those tags, but the actual names all share the
	//   same dictionary.
	// Required <enum> attributes: 'name', 'value'
	// For containing <enums> which have type="enum" or type="bitmask",
	// tag all contained <enum>s are required. This is a stopgap until
	// a better scheme for tagging core and extension enums is created.
	for _, enums := range root.FindElements("enums") {
		required := true
		if val := enums.SelectAttrValue("type", ""); val != "" {
			required = false
		}

		// enum sanity check., ie:  //         assert(type_name not in self.aliasdict)
		typeName := enums.SelectAttrValue("name", "")
		if _, ok := reg.aliasdict[typeName]; ok {
			slog.Error("Enum values are defined only for the type that is not aliased to something else")
			return // exit
		}
		for _, enum := range enums.FindElements("enum") {
			enumInfo := NewEnumInfo(enum)
			enumInfo.required = required
			enumInfo.vendor = getApiVendorTag(typeName)
			reg.addElementInfo(enum, enumInfo, "enum", reg.enumdict)
			reg.addEnumValue(enum, typeName)
		}
	}
	slog.Info("added enum info", "count", len(reg.enumdict))

	// Create dictionary of registry commands from <command> tags
	// and add 'name' attribute to each <command> tag (where missing)
	// based on its <proto><name> element.
	//
	// There is usually only one <commands> block; more are OK.
	// Required <command> attributes: 'name' or <proto><name> tag contents
	//
	// List of commands which alias others. Contains
	//   [ name, aliasName, element ]
	// for each alias
	type commandAlias struct {
		name  string
		alias string
		cmd   *etree.Element
	}
	cmdAliases := []commandAlias{}
	for _, cmd := range root.FindElements("commands/command") {
		// If the <command> does not already have a 'name' attribute, set
		// it from contents of its <proto><name> tag.
		name := cmd.SelectAttrValue("name", "")
		if name == "" {
			nameElem := cmd.FindElement("proto/name")
			if nameElem == nil || nameElem.Text() == "" {
				slog.Error("Command without a name!")
				return // abort
			}
			name = nameElem.Text()
			cmd.CreateAttr("name", name)
		}
		ci := NewCmdInfo(cmd)
		ci.vendor = getApiVendorTag(name)
		reg.addElementInfo(cmd, ci, "command", reg.cmddict)
		alias := cmd.SelectAttrValue("alias", "")
		if alias != "" {
			cmdAliases = append(cmdAliases, commandAlias{name: name, alias: alias, cmd: cmd})
			reg.aliasdict[name] = alias
		}
	}
	slog.Info("added command info", "count", len(reg.cmddict))

	// Now loop over aliases, injecting a copy of the aliased command's
	// Element with the aliased prototype name replaced with the command
	// name - if it exists.
	// Copy the 'export' sttribute (whether it exists or not) from the
	// original, aliased command, since that can be different for a
	// command and its alias.
	cmdAliasReplacementCount := 0
	for _, a := range cmdAliases {
		if cmdAlias, ok := reg.cmddict[a.alias]; ok {
			aliasInfo := cmdAlias
			cmdElem := aliasInfo.element().Copy()
			cmdElem.FindElement("proto/name").SetText(a.name)
			cmdElem.CreateAttr("name", a.name)
			cmdElem.CreateAttr("alias", a.alias)
			export := a.cmd.SelectAttrValue("export", "")
			if export != "" {
				// Replicate the command's 'export' attribute
				cmdElem.CreateAttr("export", export)
			} else {
				export := cmdElem.SelectAttrValue("export", "")

				// Remove the 'export' attribute, if the alias has one but
				// the command does not.
				if export != "" {
					cmdElem.RemoveAttr(export) // del cmdElem.attrib['export']
				}
			}

			// Replace the dictionary entry for the CmdInfo element
			reg.cmddict[a.name] = NewCmdInfo(cmdElem)
			cmdAliasReplacementCount += 1
			// slog.Info("replacing command with alias", "name", a.name, "alias", a.alias)
		} else {
			cmdName := a.cmd.SelectAttrValue("name", "")
			slog.Warn("No matching <command> found for command", "command", cmdName, "alias", a.alias)
		}
	}
	slog.Info("replaced commands with alias", "count", cmdAliasReplacementCount)

	// add features
	featureCount := 0
	featureEnumCount := 0
	for _, feature := range root.FindElements("feature") {
		featureInfo := NewFeatureInfo(feature)
		reg.addElementInfo(feature, featureInfo, "feature", reg.apidict)
		featureCount += 1

		// Add additional enums defined only in <feature> tags
		// to the corresponding enumerated type.
		// When seen here, the <enum> element, processed to contain the
		// numeric enum value, is added to the corresponding <enums>
		// element, as well as adding to the enum dictionary. It is no
		// longer removed from the <require> element it is introduced in.
		// Instead, generateRequiredInterface ignores <enum> elements
		// that extend enumerated types.
		//
		// For <enum> tags which are actually just constants, if there is
		// no 'extends' tag but there is a 'value' or 'bitpos' tag, just
		// add an EnumInfo record to the dictionary. That works because
		// output generation of constants is purely dependency-based, and
		// does not need to iterate through the XML tags.
		for _, elem := range feature.FindElements("require") {
			for _, enum := range elem.FindElements("enum") {
				addEnumInfo := false
				groupName := enum.SelectAttrValue("extends", "")
				if groupName != "" {

					// self.gen.logMsg('diag', 'Found extension enum', enum.get('name'))
					// Add version number attribute to the <enum> element
					enum.CreateAttr("version", featureInfo.version)
					// Look up the GroupInfo with matching groupName
					if gi, ok := reg.groupdict[groupName]; ok {
						gi.element().AddChild(enum.Copy()) // duplicates removed during generation.
					} else {
						enumName := enum.SelectAttrValue("name", "")
						slog.Warn("no matching group for enum", "group", groupName, "enum", enumName)
					}
					addEnumInfo = true
				} else {
					// self.gen.logMsg('diag', 'Adding extension constant "enum"', enum.get('name'))
					value := enum.SelectAttrValue("value", "")
					bitpos := enum.SelectAttrValue("bitpos", "")
					alias := enum.SelectAttrValue("alias", "")
					if value != "" || bitpos != "" || alias != "" {
						addEnumInfo = true
					}
				}
				if addEnumInfo {
					enumInfo := NewEnumInfo(enum)
					reg.addElementInfo(enum, enumInfo, "enum", reg.enumdict)
					reg.addEnumValue(enum, groupName)
					featureEnumCount += 1
				}
			}
		}
	}
	slog.Info("added features and feature enums", "features", featureCount, "enums", featureEnumCount)

	// find dependent features for platform extensions.
	requiredByExtensions := []string{}
	reg.extensions = root.FindElements("extensions/extension")
	for _, feature := range reg.extensions {
		name := feature.SelectAttrValue("name", "")
		platform := feature.SelectAttrValue("platform", "")

		// trim the extensions down to what is needed by the the supported platforms.
		if slices.Contains(platforms, platform) { // only care about platform features
			deps := getDependencies(feature)
			for _, d := range deps {
				if !slices.Contains(requiredByExtensions, d) {
					requiredByExtensions = append(requiredByExtensions, d)
					slog.Debug("extension dependency", "ext", name, "dep", d)
				}
			}
		}
	}

	extensionCount := 0
	extensionEnumCount := 0
	reg.extensions = root.FindElements("extensions/extension")
	for _, feature := range reg.extensions {
		name := feature.SelectAttrValue("name", "")
		platform := feature.SelectAttrValue("platform", "")

		// trim the extensions down to what is needed by the the supported platforms.
		switch {
		case slices.Contains(platforms, platform):
			// keep platform features
		case slices.Contains(requiredByExtensions, name): // keep
			// keep platform feature dependencies
		default:
			continue // ignore everything else
		}
		slog.Debug("adding extension", "name", name, "platform", platform)

		// add the extension as a feature.
		featureInfo := NewFeatureInfo(feature)
		reg.addElementInfo(feature, featureInfo, "extension", reg.extdict)
		extensionCount += 1

		// Add additional enums defined only in <extension> tags
		// to the corresponding core type.
		// Algorithm matches that of enums in a "feature" tag as above.
		//
		// This code also adds a 'extnumber' attribute containing the
		// extension number, used for enumerant value calculation.
		for _, elem := range feature.FindElements("require") {
			for _, enum := range elem.FindElements("enum") {
				enumName := enum.SelectAttrValue("name", "")
				addEnumInfo := false
				groupName := enum.SelectAttrValue("extends", "")
				if groupName != "" {
					// Add <extension> block's extension number attribute to
					// the <enum> element unless specified explicitly, such
					// as when redefining an enum in another extension.
					extnumber := enum.SelectAttrValue("extnumber", "")
					if extnumber == "" {
						enum.CreateAttr("extnumber", strconv.Itoa(featureInfo.number))
					}
					enum.CreateAttr("extname", featureInfo.name)
					enum.CreateAttr("supported", featureInfo.supported)

					// Look up the GroupInfo with matching groupName
					if gi, ok := reg.groupdict[groupName]; ok {
						gi.element().AddChild(enum.Copy()) // duplicates removed during generation.
					} else {
						slog.Warn("NO matching group for enum", "group", groupName, "enum", enumName)
					}
					addEnumInfo = true
				} else {
					value := enum.SelectAttrValue("value", "")
					bitpos := enum.SelectAttrValue("bitpos", "")
					alias := enum.SelectAttrValue("alias", "")
					if value != "" || bitpos != "" || alias != "" {
						addEnumInfo = true // Adding extension constant "enum"
					}
				}
				if addEnumInfo {
					enumInfo := NewEnumInfo(enum)
					reg.addElementInfo(enum, enumInfo, "enum", reg.enumdict)
					reg.addEnumValue(enum, groupName)
					extensionEnumCount += 1
				}
			}
		}
	}
	slog.Info("added extension features and enums", "features", extensionCount, "enums", extensionEnumCount)

	// ----------------------------------------
	// FUTURE: add spirv/sync block as needed.
	// ----------------------------------------
}

// markTypeRequired :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Require (along with its dependencies) or remove (but not its dependencies) a type.
func (reg *Registry) markTypeRequired(typename string, required bool) {

	// Get TypeInfo object for <type> tag corresponding to typename
	info := reg.lookupElementInfo(typename, reg.typedict)
	typeinfo := info.(*TypeInfo)
	if typeinfo != nil {
		if required {

			// Tag type dependencies in 'alias' and 'required' attributes as
			// required. This does not un-tag dependencies in a <remove>
			// tag. See comments in markRequired() below for the reason.
			for _, attribName := range []string{"requires", "alias"} {
				depname := typeinfo.elem.SelectAttrValue(attribName, "")
				if depname != "" {

					// Do not recurse on self-referential structures.
					if typename != depname {
						reg.markTypeRequired(depname, required)
					} else {
						// self.gen.logMsg('diag', 'type', typename, 'is self-referential')
					}
				}
			}

			// Tag types used in defining this type (e.g. in nested <type> tags)
			// Look for <type> in entire <command> tree, not just immediate children
			for _, subtype := range typeinfo.elem.FindElements(".//type") {
				if typename != subtype.Text() {
					reg.markTypeRequired(subtype.Text(), required)
				} else {
					// self.gen.logMsg('diag', 'type', typename, 'is self-referential')
				}
			}

			// Tag enums used in defining this type, for example in
			// <member><name>member</name>[<enum>MEMBER_SIZE</enum>]</member>
			for _, subenum := range typeinfo.elem.FindElements(".//enum") {
				//     self.gen.logMsg('diag', 'markRequired: type requires dependent <enum>', subenum.text)
				reg.markEnumRequired(subenum.Text(), required)
			}

			// Tag type dependency in 'bitvalues' attributes as
			// required. This ensures that the bit values for a flag
			// are emitted
			depType := typeinfo.elem.SelectAttrValue("bitvalues", "")
			if depType != "" {
				reg.markTypeRequired(depType, required)
			}
		}
		typeinfo.required = required
	} else {
		//	TODO check elif '.h' not in typename:
		slog.Warn("type IS NOT DEFINED", "type", typename)
	}
}

// markEnumRequired :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Mark an enum as required or not.
func (reg *Registry) markEnumRequired(enumname string, required bool) {
	info := reg.lookupElementInfo(enumname, reg.enumdict)
	if info != nil {
		enum := info.(*EnumInfo)

		// If the enum is part of a group, and is being removed, then
		// look it up in that <enums> tag and remove the Element there,
		// so that it is not visible to generators (which traverse the
		// <enums> tag elements rather than using the dictionaries).
		// Only attempt removal when it was previously required; enums that
		// were never required (e.g. in a require block with unsatisfied
		// depends) stay in the group but will not be emitted because the
		// group's required logic in generateFeature also checks EnumInfo.
		if !required && enum.required {
			groupName := enum.elem.SelectAttrValue("extends", "")
			if groupName != "" {
				// Look up the Info with matching groupName
				if groupInfo, ok := reg.groupdict[groupName]; ok {
					gi := groupInfo.(*GroupInfo)
					gienum := gi.elem.FindElement("enum[@name=" + enumname + "]")
					if gienum != nil {
						// Remove copy of this enum from the group
						gienum.Parent().RemoveChild(gienum)
					} else {
						slog.Warn("markEnumRequired: Cannot remove enum not in group", "enum", enumname, "group", groupName)
					}
				} else {
					slog.Warn("markEnumRequired: Cannot remove enum from nonexistent group", "enum", enumname, "group", groupName)
				}
			} else {
				// This enum is not an extending enum.
				// The XML tree must be searched for all <enums> that
				// might have it, so we know the parent to delete from.
				enumName := enum.elem.SelectAttrValue("name", "")
				count := 0
				for _, enums := range reg.tree.FindElements("enums") {
					for _, thisEnum := range enums.FindElements("enum") {
						thisName := thisEnum.SelectAttrValue("name", "")
						if thisName == enumName {
							// Actually remove it
							count = count + 1
							thisEnum.Parent().RemoveChild(thisEnum)
						}
					}
				}
				if count == 0 {
					slog.Warn("markEnumRequired: enum not found in and <enums> tag", "enum", "enumName")
				}
			}
		}
		enum.required = required

		// Tag enum dependencies in 'alias' attribute as required
		depname := enum.elem.SelectAttrValue("alias", "")
		if depname != "" {
			reg.markEnumRequired(depname, required)
		}
	} else {
		slog.Warn("enum IS NOT DEFINED", "enum", enumname)
	}
}

// markCmdRequired :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Mark a command as required or not.
func (reg *Registry) markCmdRequired(cmdname string, required bool) {
	cmdInfo := reg.lookupElementInfo(cmdname, reg.cmddict)
	if cmdInfo != nil {
		cmd := cmdInfo.(*CmdInfo)
		cmd.required = required

		// TODO check if needed for vulkan.
		// Tag command dependencies in 'alias' attribute as required
		//
		// This is usually not done, because command 'aliases' are not
		// actual C language aliases like type and enum aliases. Instead
		// they are just duplicates of the function signature of the
		// alias. This means that there is no dependency of a command
		// alias on what it aliases. One exception is validity includes,
		// where the spec markup needs the promoted-to validity include
		// even if only the promoted-from command is being built.
		//if self.genOpts.requireCommandAliases:
		//    depname = cmd.elem.get('alias')
		//    if depname:
		//        self.gen.logMsg('diag', 'Generating dependent command',
		//                        depname, 'for alias', cmdname)
		//        self.markCmdRequired(depname, required)

		// Tag all parameter types of this command as required.
		// This does not remove types of commands in a <remove>
		// tag, because many other commands may use the same type.
		// We could be more clever and reference count types,
		// instead of using a boolean.
		if required {
			// Look for <type> in entire <command> tree,
			// not just immediate children
			for _, typeElem := range cmd.elem.FindElements(".//type") {
				// self.gen.logMsg('diag', 'markRequired: command implicitly requires dependent type', type_elem.text)
				reg.markTypeRequired(typeElem.Text(), required)
			}
		}
	} else {
		slog.Warn("command IS NOT DEFINED", "command", cmdname)
	}
}

// markRequired :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Require or remove features specified in the Element.
func (reg *Registry) markRequired(featurename string, feature *etree.Element, required bool) {
	// Loop over types, enums, and commands in the tag
	// @@ It would be possible to respect 'api' and 'profile' attributes
	//  in individual features, but that is not done yet.
	for _, typeElem := range feature.FindElements("type") {
		name := typeElem.SelectAttrValue("name", "")
		reg.markTypeRequired(name, required)
	}
	for _, enumElem := range feature.FindElements("enum") {
		name := enumElem.SelectAttrValue("name", "")
		reg.markEnumRequired(name, required)
	}
	for _, cmdElem := range feature.FindElements("command") {
		name := cmdElem.SelectAttrValue("name", "")
		reg.markCmdRequired(name, required)
	}

	// Extensions may need to extend existing commands or other items in the future.
	// So, look for extend tags.
	for _, extendElem := range feature.FindElements("extend") {
		extendType := extendElem.SelectAttrValue("type", "")
		if extendType == "command" {
			// commandName := extendElem.SelectAttrValue("name", "")
			successExtends := extendElem.SelectAttrValue("successcodes", "")
			if successExtends != "" {
				slog.Error("TODO: add command success extends", "successcodes", successExtends)
			}
			errorExtends := extendElem.SelectAttrValue("errorcodes", "")
			if errorExtends != "" {
				slog.Error("TODO: add command error extends", "errorcodes", errorExtends)
			}
		} else {
			slog.Warn("extend type IS NOT SUPPORTED", "extendType", extendType)
		}
	}
}

// getAlias :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Check for an alias in the same require block.
func (reg *Registry) getAlias(elem *etree.Element, dict map[string]Info) string {
	alias := elem.SelectAttrValue("alias", "")
	if alias == "" {
		name := elem.SelectAttrValue("name", "")
		info := reg.lookupElementInfo(name, dict)
		if info == nil {
			slog.Error("alias is not a known name", "alias", name)
			return ""
		}
		typeinfo := info.(*TypeInfo)
		alias = typeinfo.elem.SelectAttrValue("alias", "")
	}
	return alias
}

// requireFeatures :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
func (reg *Registry) requireFeatures(fi *etree.Element, featurename, api, profile string) {
	// <require> marks things that are required by this version/profile
	for _, feature := range fi.FindElements("require") {
		if matchAPIProfile(api, profile, feature) {
			reg.markRequired(featurename, feature, true)
		}
	}
}

// deprecateFeatures :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
func (reg *Registry) deprecateFeatures(fi *etree.Element, featurename, api, profile string) {
	// hack to check if the feature name is an api version.
	versionmatch := strings.Contains(featurename, "_VERSION_")
	// the real way is to use the regex's from the spec_tools/conventions.py
	// versionmatch := APIConventions().is_api_version_name(featurename)

	// <deprecate> marks things that are deprecated by this version/profile
	for _, deprecation := range fi.FindElements("deprecate") {
		if matchAPIProfile(api, profile, deprecation) {
			for _, typeElem := range deprecation.FindElements("type") {

				// First check for a group, use that preferentially
				name := typeElem.SelectAttrValue("name", "")
				info := reg.lookupElementInfo(name, reg.groupdict)
				if info != nil {
					typeInfo := info.(*GroupInfo)
					supersededby := typeElem.SelectAttrValue("supersededby", "")
					if typeInfo.supersededby != "" && supersededby != "" && typeInfo.supersededby != supersededby {
						slog.Error("type is tagged for deprecation twice but with different supersededby attributes",
							"type", name, "supersededby", supersededby)
					} else {
						typeInfo.supersededby = supersededby
					}
					if versionmatch {
						typeInfo.deprecatedbyversion = featurename
					} else {
						typeInfo.deprecatedbyextensions = append(typeInfo.deprecatedbyextensions, featurename)
					}
					typeInfo.deprecatedlink = deprecation.SelectAttrValue("explanationlink", "")
				} else {
					info = reg.lookupElementInfo(name, reg.typedict)
					if info != nil {
						typeInfo := info.(*TypeInfo)
						supersededby := typeElem.SelectAttrValue("supersededby", "")
						if typeInfo.supersededby != "" && supersededby != "" && typeInfo.supersededby != supersededby {
							slog.Error("type is tagged for deprecation twice but with different supersededby attributes",
								"type", name, "supersededby", supersededby)
						} else {
							typeInfo.supersededby = supersededby
						}
						if versionmatch {
							typeInfo.deprecatedbyversion = featurename
						} else {
							typeInfo.deprecatedbyextensions = append(typeInfo.deprecatedbyextensions, featurename)
						}
						typeInfo.deprecatedlink = deprecation.SelectAttrValue("explanationlink", "")
					} else {
						slog.Error("type is tagged for deprecation but not present in registry", "type", name)
					}
				}
			}

			for _, enumElem := range deprecation.FindElements("enum") {
				name := enumElem.SelectAttrValue("name", "")
				info := reg.lookupElementInfo(name, reg.enumdict)
				if info != nil {
					enum := info.(*EnumInfo)
					supersededby := enumElem.SelectAttrValue("supersededby", "")
					if enum.supersededby != "" && supersededby != "" && enum.supersededby != supersededby {
						slog.Error("enum is tagged for deprecation twice but with different supersededby attributes",
							"enum", name, "supersededby", supersededby)
					} else {
						enum.supersededby = supersededby
					}
					if versionmatch {
						enum.deprecatedbyversion = featurename
					} else {
						enum.deprecatedbyextensions = append(enum.deprecatedbyextensions, featurename)
					}
					enum.deprecatedlink = deprecation.SelectAttrValue("explanationlink", "")
				} else {
					slog.Error("enum is tagged for deprecation but not present in registry", "enum", name)
				}
			}

			for _, cmdElem := range deprecation.FindElements("command") {
				name := cmdElem.SelectAttrValue("name", "")
				info := reg.lookupElementInfo(name, reg.cmddict)
				if info != nil {
					cmd := info.(*CmdInfo)
					supersededby := cmdElem.SelectAttrValue("supersededby", "")
					if cmd.supersededby != "" && supersededby != "" && cmd.supersededby != supersededby {
						slog.Error("command is tagged for deprecation twice but with different supersededby attributes",
							"command", name, "supersededby", supersededby)
					} else {
						cmd.supersededby = supersededby
					}
					if versionmatch {
						cmd.deprecatedbyversion = featurename
					} else {
						cmd.deprecatedbyextensions = append(cmd.deprecatedbyextensions, featurename)
					}
					cmd.deprecatedlink = deprecation.SelectAttrValue("explanationlink", "")
				} else {
					slog.Error("command is tagged for deprecation but not present in registry", "command", name)
				}
			}
		}
	}
}

// assignAdditionalValidity :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
func (reg *Registry) assignAdditionalValidity(fi *etree.Element, api, profile string) {
	// Loop over all usage inside all <require> tags.
	for _, feature := range fi.FindElements("require") {
		if matchAPIProfile(api, profile, feature) {
			for _, v := range feature.FindElements("usage") {
				if cmd := v.SelectAttrValue("command", ""); cmd != "" {
					info := reg.cmddict[cmd]
					cmd := info.(*CmdInfo)
					cmd.additionalValidity = append(cmd.additionalValidity, v.Copy())
				}
				if str := v.SelectAttrValue("struct", ""); str != "" {
					info := reg.typedict[str]
					typeInfo := info.(*TypeInfo)
					typeInfo.additionalValidity = append(typeInfo.additionalValidity, v.Copy())
				}
			}
		}
	}
}

// removeAdditionalValidity :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
func (reg *Registry) removeAdditionalValidity(fi *etree.Element, api, profile string) {
	// Loop over all usage inside all <remove> tags.
	for _, feature := range fi.FindElements("remove") {
		if matchAPIProfile(api, profile, feature) {
			for _, v := range feature.FindElements("usage") {
				if cmd := v.SelectAttrValue("command", ""); cmd != "" {
					info := reg.cmddict[cmd]
					cmd := info.(*CmdInfo)
					cmd.removedValidity = append(cmd.removedValidity, v.Copy())
				}
				if str := v.SelectAttrValue("struct", ""); str != "" {
					info := reg.typedict[str]
					typeInfo := info.(*TypeInfo)
					typeInfo.removedValidity = append(typeInfo.removedValidity, v.Copy())
				}
			}
		}
	}
}

// generateFeature :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Generate a single type / enum group / enum / command,
// and all its dependencies as needed.
func (reg *Registry) generateFeature(fname, ftype string, dictionary map[string]Info, explicit bool) {
	f := reg.lookupElementInfo(fname, dictionary)
	if f == nil {
		slog.Error("generateFeature: no entry found for feature", "feature", fname)
		return
	}

	// If feature is not required, or has already been declared, return
	if !f.isRequired() {
		return // self.gen.logMsg('diag', 'Skipping', ftype, fname, '(not required)')
	}
	if f.isDeclared() {
		return // self.gen.logMsg('diag', 'Skipping', ftype, fname, '(already declared)')
	}

	// Always mark feature declared, as though actually emitted
	f.setDeclared(true)

	// Determine if this is an alias, and of what, if so
	alias := f.element().SelectAttrValue("alias", "")

	// Pull in dependent declaration(s) of the feature.
	// For types, there may be one type in the 'requires' attribute of
	//   the element, one in the 'alias' attribute, and many in
	//   embedded <type> and <enum> tags within the element.
	// For commands, there may be many in <type> tags within the element.
	// For enums, no dependencies are allowed (though perhaps if you
	//   have a uint64 enum, it should require that type).
	var genProc func(info Info, typeName, alias string)
	followupFeature := ""
	if ftype == "type" {
		genProc = reg.gen.genType

		// Generate type dependencies in 'alias' and 'requires' attributes
		if alias != "" {
			reg.generateFeature(alias, "type", reg.typedict, false)
		}
		requires := f.element().SelectAttrValue("requires", "")
		if requires != "" {
			// self.gen.logMsg('diag', 'Generating required dependent type', requires)
			reg.generateFeature(requires, "type", reg.typedict, false)
		}

		// Generate types used in defining this type (e.g. in nested <type> tags)
		// Look for <type> in entire <command> tree, not just immediate children
		for _, subtype := range f.element().FindElements(".//type") {
			reg.generateFeature(subtype.Text(), "type", reg.typedict, false)
		}

		// Generate enums used in defining this type, for example in
		//   <member><name>member</name>[<enum>MEMBER_SIZE</enum>]</member>
		for _, subtype := range f.element().FindElements(".//enum") {
			reg.generateFeature(subtype.Text(), "enum", reg.enumdict, false)
		}

		// If the type is an enum group, look up the corresponding
		// group in the group dictionary and generate that instead.
		if category := f.element().SelectAttrValue("category", ""); category == "enum" {
			groupInfo := reg.lookupElementInfo(fname, reg.groupdict)
			if alias != "" {

				// An alias of another group name.
				// Pass to genGroup with 'alias' parameter = aliased name
				// self.gen.logMsg('diag', 'Generating alias', fname, 'for enumerated type', alias)
				//
				// Now, pass the *aliased* GroupInfo to the genGroup, but
				// with an additional parameter which is the alias name.
				f = reg.lookupElementInfo(alias, reg.groupdict)
				genProc = reg.gen.genGroup
			} else if groupInfo == nil {
				slog.Warn("Skipping enum type: no matching enumerant group", "enum", fname)
				return
			} else {
				genProc = reg.gen.genGroup
				f = groupInfo
				group := groupInfo.(*GroupInfo)

				// The enum group is not ready for generation. At this
				// point, it contains all <enum> tags injected by
				// <extension> tags without any verification of whether
				// they are required or not. It may also contain
				// duplicates injected by multiple consistent
				// definitions of an <enum>.
				//
				// Pass over each enum, marking its enumdict[] entry as
				// required or not. Mark aliases of enums as required,
				// too.
				enums := group.elem.FindElements("enum")

				// Check for required enums, including aliases
				// LATER - Check for, report, and remove duplicates?
				for _, elem := range enums {
					elem.CreateAttr("required", "true")
				}
			}
		}
		if f.element().SelectAttrValue("category", "") == "bitmask" {
			followupFeature = f.element().SelectAttrValue("bitvalues", "")
		}
	} else if ftype == "command" {

		// Generate command dependencies in 'alias' attribute
		if alias != "" {
			reg.generateFeature(alias, "command", reg.cmddict, false)
		}
		genProc = reg.gen.genCmd
		for _, typeElem := range f.element().FindElements(".//type") {
			depname := typeElem.Text()
			reg.generateFeature(depname, "type", reg.typedict, false)
		}
	} else if ftype == "enum" {

		// Generate enum dependencies in 'alias' attribute
		if alias != "" {
			reg.generateFeature(alias, "enum", reg.enumdict, false)
		}
		genProc = reg.gen.genEnum
	}

	// Actually generate the type
	if genProc == nil {
		slog.Error("genProc is nil")
		return
	}
	genProc(f, fname, alias)
	if followupFeature != "" {
		reg.generateFeature(followupFeature, "type", reg.typedict, false)
	}
}

// generateRequiredInterface :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Generate all interfaces required by an API version or extension.
// - interface - Element for `<version>` or `<extension>`
func (reg *Registry) generateRequiredInterface(iface *etree.Element) {

	// Loop over all features inside all <require> tags.
	for _, features := range iface.FindElements("require") {
		for _, t := range features.FindElements("type") {
			name := t.SelectAttrValue("name", "")
			reg.generateFeature(name, "type", reg.typedict, true)
		}
		for _, e := range features.FindElements("enum") {
			name := e.SelectAttrValue("name", "")

			// If this is an enum extending an enumerated type, do not
			// generate it - this has already been done in reg.parseTree,
			// by copying this element into the enumerated type.
			enumextends := e.SelectAttrValue("extends", "")
			if enumextends == "" {
				reg.generateFeature(name, "enum", reg.enumdict, true)
			}
		}
		for _, c := range features.FindElements("command") {
			name := c.SelectAttrValue("name", "")
			reg.generateFeature(name, "command", reg.cmddict, true)
		}
	}
}

// stripUnsupportedAPIs :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Strip unsupported APIs from attributes of APIs.
// dictionary - *Info dictionary of APIs to be updated
// attribute - attribute name to look for in each API
// supportedDictionary - dictionary in which to look for supported
func (reg *Registry) stripUnsupportedAPIs(dictionary map[string]Info, attribute string, supportedDictionary map[string]Info) {
	for _, info := range dictionary {
		attribstring := info.element().SelectAttrValue(attribute, "")
		if attribstring != "" {
			apis := []string{}
			stripped := false
			for _, api := range strings.Split(attribstring, ",") {
				_, ok := supportedDictionary[api]
				if ok && supportedDictionary[api].isRequired() {
					apis = append(apis, api)
				} else {
					stripped = true
				}

				// Update the attribute after stripping stuff.
				// Could sort apis before joining, but it is not a clear win
				if stripped {
					info.element().CreateAttr(attribute, strings.Join(apis, ","))
				}
			}
		}
	}
}

// ----------------------------------------
// FUTURE: add other methods as needed.
// ----------------------------------------

// apiGen :: https://github.com/KhronosGroup/Vulkan-Docs/blob/main/scripts/reg.py.
//
// Generate interface for specified versions using the current
// generator and generator options
func (reg *Registry) apiGen(selectedAPI string) {
	featureList := map[string]FeatureInfo{}
	sortedFeatures := []string{}

	// Get all matching API feature names & add to list of FeatureInfo
	sortedAPIs := []string{}
	for f, _ := range reg.apidict {
		sortedAPIs = append(sortedAPIs, f)
	}
	slices.Sort(sortedAPIs)
	apiMatch := false
	for _, apiName := range sortedAPIs {
		info := reg.apidict[apiName]
		api := info.element().SelectAttrValue("api", "")
		if apiNameMatch(selectedAPI, api) {
			fi, ok := info.(*FeatureInfo)
			if !ok {
				slog.Error("expected FeatureInfo")
				continue
			}
			apiMatch = true
			slog.Debug("generating feature", "feat", fi.name)
			sortedFeatures = append(sortedFeatures, apiName)
			featureList[fi.name] = *fi
		}
	}
	if !apiMatch {
		slog.Info("No matching API versions found!")
	}

	// Get all matching extensions, in order by their extension number,
	// and add to the features list.
	// Start with extensions whose 'supported' attributes match the API
	// being generated. Add extensions matching the pattern specified in
	// regExtensions, then remove extensions matching the pattern
	// specified in regRemoveExtensions
	sortedExtensions := []string{}
	for name, _ := range reg.extdict {
		sortedExtensions = append(sortedExtensions, name)
	}
	slices.Sort(sortedExtensions)
	for _, extName := range sortedExtensions {
		ei := reg.extdict[extName].(*FeatureInfo)

		// use all extensions that support vulkan.
		supported := ei.elem.SelectAttrValue("supported", "")
		if strings.Contains(supported, selectedAPI) {
			slog.Debug("generating extension", "ext", ei.name)
			sortedFeatures = append(sortedFeatures, extName)
			featureList[ei.name] = *ei
		}
	}

	// Passes 1+2: loop over requested API versions and extensions tagging
	//   types/commands/features as required (in an <require> block) or no
	//   longer required (in an <remove> block). <remove>s are processed
	//   after all <require>s, so removals win.
	// If a profile other than 'None' is being generated, it must
	//   match the profile attribute (if any) of the <require> and
	//   <remove> tags.
	apiname, profile := "vulkan", ""
	slog.Info("PASS 1: TAG FEATURES")
	for _, featureKey := range sortedFeatures {
		f := featureList[featureKey]
		reg.requireFeatures(f.elem, f.name, apiname, profile)
		reg.deprecateFeatures(f.elem, f.name, apiname, profile)
		reg.assignAdditionalValidity(f.elem, apiname, profile)
	}

	slog.Info("PASS 2: Tagging removed features")
	for _, featureKey := range sortedFeatures {
		f := featureList[featureKey]
		reg.removeAdditionalValidity(f.elem, apiname, profile)
	}

	// Now, strip references to APIs that are not required.
	// At present such references may occur in:
	// Structs in <type category="struct"> 'structextends' attributes
	// Enums in <command> 'successcodes' and 'errorcodes' attributes
	reg.stripUnsupportedAPIs(reg.typedict, "structextends", reg.typedict)
	reg.stripUnsupportedAPIs(reg.cmddict, "successcodes", reg.enumdict)
	reg.stripUnsupportedAPIs(reg.cmddict, "errorcodes", reg.enumdict)
	// reg.stripUnsupportedAPIsFromList(reg.validextensionstructs, reg.typedict)

	// Construct lists of valid extension structures
	// self.tagValidExtensionStructs()

	// Pass 3: loop over specified API versions and extensions printing
	//   declarations for required things which have not already been
	//   generated.
	slog.Info("PASS 3: GENERATE INTERFACES FOR FEATURES")
	emit := true
	reg.gen.beginFile()
	for _, featureKey := range sortedFeatures {
		f := featureList[featureKey]

		// Generate the interface
		reg.gen.beginFeature(f.elem, emit)
		reg.generateRequiredInterface(f.elem)
		reg.gen.endFeature()
	}
	reg.gen.endFile()
}
