package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

const (
	pythonEndpointFile = "sdk_python/bunq/sdk/model/generated/endpoint.py"
	pythonObjectFile   = "sdk_python/bunq/sdk/model/generated/object_.py"

	outputObjectsFile   = "objects_gen.go"
	outputEndpointsFile = "endpoints_gen.go"
	outputServicesFile  = "services_gen.go"
)

// Parsed Python class information
type pyClass struct {
	name       string // e.g. "PaymentApiObject"
	goName     string // e.g. "Payment"
	isEndpoint bool
	isAnchor   bool

	// Docstring field types: maps python field name (without _) to python type
	docFields     map[string]string
	initDocFields map[string]string // from __init__ docstring (request-specific types)

	// Response fields (from _field = None lines)
	responseFields []pyField

	// Request fields (from _field_for_request = None lines)
	requestFields []pyField

	// __init__ params
	initParams []initParam

	// Endpoint URL constants
	urlCreate  string
	urlRead    string
	urlListing string
	urlUpdate  string
	urlDelete  string

	// Object type constants
	objectTypePost string
	objectTypeGet  string
	objectTypePut  string

	// FIELD_* constants: maps FIELD_NAME to "json_name"
	fieldConstants map[string]string

	// Methods present
	hasCreate bool
	hasGet    bool
	hasList   bool
	hasUpdate bool
	hasDelete bool

	// Create return type detection
	createReturnsID     bool
	createReturnsUUID   bool
	createReturnsObject bool

	// Update return type
	updateReturnsObject bool
	updateReturnsID     bool
}

type pyField struct {
	pythonName string // e.g. "id_", "amount"
	goName     string
	goType     string
	jsonTag    string
}

type initParam struct {
	pythonName string
	goType     string
	hasDefault bool // True if param has default=None (optional)
}

func main() {
	// Parse objects
	objectContent, err := os.ReadFile(pythonObjectFile)
	if err != nil {
		fatal("reading object file: %v", err)
	}
	objectClasses := parseClasses(string(objectContent), false)

	// Parse endpoints
	endpointContent, err := os.ReadFile(pythonEndpointFile)
	if err != nil {
		fatal("reading endpoint file: %v", err)
	}
	endpointClasses := parseClasses(string(endpointContent), true)

	// Build type registry for resolving references
	typeRegistry := buildTypeRegistry(objectClasses, endpointClasses)

	// Post-process: resolve unknown types to any
	for _, c := range objectClasses {
		resolveTypes(c, typeRegistry)
	}
	for _, c := range endpointClasses {
		resolveTypes(c, typeRegistry)
	}

	// Find object names that also exist in endpoints (endpoints win)
	endpointNames := map[string]bool{}
	for _, c := range endpointClasses {
		endpointNames[c.goName] = true
	}

	// Filter out objects that are redeclared in endpoints
	var filteredObjects []*pyClass
	for _, c := range objectClasses {
		if !endpointNames[c.goName] {
			filteredObjects = append(filteredObjects, c)
		}
	}

	// Generate files
	generateObjectsFile(filteredObjects, typeRegistry)
	generateEndpointsFile(endpointClasses, typeRegistry)
	generateServicesFile(endpointClasses)

	fmt.Println("Code generation complete!")
	fmt.Printf("  Objects: %d types\n", len(objectClasses))
	fmt.Printf("  Endpoints: %d types\n", len(endpointClasses))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// parseClasses splits Python source into class declarations and parses each.
func parseClasses(content string, isEndpoint bool) []*pyClass {
	// Split by class declarations
	classRegex := regexp.MustCompile(`(?m)^class (\w+)\(([^)]*)\):`)
	matches := classRegex.FindAllStringSubmatchIndex(content, -1)

	var classes []*pyClass
	for i, m := range matches {
		className := content[m[2]:m[3]]
		bases := content[m[4]:m[5]]

		// Extract class body
		start := m[0]
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		body := content[start:end]

		pc := parseClass(className, bases, body, isEndpoint)
		if pc != nil {
			classes = append(classes, pc)
		}
	}

	return classes
}

func parseClass(className, bases, body string, isEndpoint bool) *pyClass {
	goName := pythonClassToGoName(className)
	if goName == "" {
		return nil
	}

	pc := &pyClass{
		name:           className,
		goName:         goName,
		isEndpoint:     isEndpoint,
		isAnchor:       strings.Contains(bases, "AnchorObjectInterface"),
		docFields:      make(map[string]string),
		fieldConstants: make(map[string]string),
	}

	// Parse class docstring
	parseDocstring(body, pc)

	// Parse field assignments
	parseFields(body, pc)

	// Parse __init__ parameters
	parseInit(body, pc)

	if isEndpoint {
		parseEndpointConstants(body, pc)
		parseMethods(body, pc)
	}

	return pc
}

func parseDocstring(body string, pc *pyClass) {
	// Find class docstring between first """ and next """
	docStart := strings.Index(body, `"""`)
	if docStart < 0 {
		return
	}
	docEnd := strings.Index(body[docStart+3:], `"""`)
	if docEnd < 0 {
		return
	}
	docstring := body[docStart+3 : docStart+3+docEnd]

	// Extract :type _field: type lines
	typeRegex := regexp.MustCompile(`:type (_\w+):\s*(.+)`)
	for _, match := range typeRegex.FindAllStringSubmatch(docstring, -1) {
		fieldName := strings.TrimPrefix(match[1], "_")
		fieldName = strings.TrimSuffix(fieldName, "_field_for_request")
		pyType := strings.TrimSpace(match[2])
		pc.docFields[fieldName] = pyType
	}

	// Also parse __init__ docstring for request field types (may differ from class docstring)
	pc.initDocFields = parseInitDocstring(body)
}

// initDocFields maps field name (without _) to python type from __init__ docstring
func parseInitDocstring(body string) map[string]string {
	result := map[string]string{}

	// Find __init__ method
	initIdx := strings.Index(body, "def __init__")
	if initIdx < 0 {
		return result
	}

	// Find docstring inside __init__
	bodyAfterInit := body[initIdx:]
	docStart := strings.Index(bodyAfterInit, `"""`)
	if docStart < 0 {
		return result
	}
	docEnd := strings.Index(bodyAfterInit[docStart+3:], `"""`)
	if docEnd < 0 {
		return result
	}
	docstring := bodyAfterInit[docStart+3 : docStart+3+docEnd]

	typeRegex := regexp.MustCompile(`:type (\w+):\s*(.+)`)
	for _, match := range typeRegex.FindAllStringSubmatch(docstring, -1) {
		fieldName := match[1]
		pyType := strings.TrimSpace(match[2])
		result[fieldName] = pyType
	}

	return result
}

func parseFields(body string, pc *pyClass) {
	// Response fields: _field = None (but NOT _field_for_request)
	responseRegex := regexp.MustCompile(`(?m)^\s+_(\w+)\s*=\s*None\s*$`)
	for _, match := range responseRegex.FindAllStringSubmatch(body, -1) {
		fieldName := match[1]
		if strings.HasSuffix(fieldName, "_field_for_request") {
			continue
		}
		// Look up type from docstring
		pyType := pc.docFields[fieldName]
		goType := pythonTypeToGo(pyType, false)
		goFieldName := snakeToPascal(fieldName)
		jsonTag := strings.TrimSuffix(strings.TrimPrefix(fieldName, "_"), "_")

		pc.responseFields = append(pc.responseFields, pyField{
			pythonName: fieldName,
			goName:     goFieldName,
			goType:     goType,
			jsonTag:    jsonTag,
		})
	}

	// Request fields: _field_for_request = None
	requestRegex := regexp.MustCompile(`(?m)^\s+_(\w+)_field_for_request\s*=\s*None\s*$`)
	for _, match := range requestRegex.FindAllStringSubmatch(body, -1) {
		fieldName := match[1]
		// Prefer init docstring types (request-specific) over class docstring types
		pyType := pc.initDocFields[fieldName]
		if pyType == "" {
			pyType = pc.docFields[fieldName]
		}
		goType := pythonTypeToGo(pyType, true)
		goFieldName := snakeToPascal(fieldName)
		jsonTag := strings.TrimSuffix(strings.TrimPrefix(fieldName, "_"), "_")

		pc.requestFields = append(pc.requestFields, pyField{
			pythonName: fieldName,
			goName:     goFieldName,
			goType:     goType,
			jsonTag:    jsonTag,
		})
	}
}

func parseInit(body string, pc *pyClass) {
	// Find __init__ method signature
	initRegex := regexp.MustCompile(`def __init__\(self,\s*([^)]+)\)`)
	match := initRegex.FindStringSubmatch(body)
	if match == nil {
		return
	}

	params := splitParams(match[1])
	for _, param := range params {
		param = strings.TrimSpace(param)
		hasDefault := strings.Contains(param, "=")
		paramName := strings.Split(param, "=")[0]
		paramName = strings.TrimSpace(paramName)

		pyType := pc.docFields[paramName]
		goType := pythonTypeToGo(pyType, true)

		pc.initParams = append(pc.initParams, initParam{
			pythonName: paramName,
			goType:     goType,
			hasDefault: hasDefault,
		})
	}
}

func splitParams(s string) []string {
	// Split params, but be careful with nested brackets
	var params []string
	depth := 0
	start := 0
	for i, ch := range s {
		switch ch {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				params = append(params, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		params = append(params, s[start:])
	}
	return params
}

func parseEndpointConstants(body string, pc *pyClass) {
	// URL constants
	urlRegex := regexp.MustCompile(`_ENDPOINT_URL_(\w+)\s*=\s*"([^"]+)"`)
	for _, match := range urlRegex.FindAllStringSubmatch(body, -1) {
		switch match[1] {
		case "CREATE":
			pc.urlCreate = match[2]
		case "READ":
			pc.urlRead = match[2]
		case "LISTING":
			pc.urlListing = match[2]
		case "UPDATE":
			pc.urlUpdate = match[2]
		case "DELETE":
			pc.urlDelete = match[2]
		}
	}

	// Object type constants
	objTypeRegex := regexp.MustCompile(`_OBJECT_TYPE_(\w+)\s*=\s*"([^"]+)"`)
	for _, match := range objTypeRegex.FindAllStringSubmatch(body, -1) {
		switch match[1] {
		case "POST":
			pc.objectTypePost = match[2]
		case "GET":
			pc.objectTypeGet = match[2]
		case "PUT":
			pc.objectTypePut = match[2]
		}
	}

	// Field constants
	fieldRegex := regexp.MustCompile(`FIELD_(\w+)\s*=\s*"([^"]+)"`)
	for _, match := range fieldRegex.FindAllStringSubmatch(body, -1) {
		pc.fieldConstants[match[1]] = match[2]
	}
}

func parseMethods(body string, pc *pyClass) {
	// Check which methods exist
	if regexp.MustCompile(`def create\(cls`).MatchString(body) {
		pc.hasCreate = true
		// Determine return type
		if strings.Contains(body, "_process_for_id(response_raw)") {
			pc.createReturnsID = true
		} else if strings.Contains(body, "_process_for_uuid(response_raw)") {
			pc.createReturnsUUID = true
		} else if strings.Contains(body, "_from_json(response_raw") {
			pc.createReturnsObject = true
		}
	}
	if regexp.MustCompile(`def get\(cls`).MatchString(body) {
		pc.hasGet = true
	}
	if regexp.MustCompile(`def list\(cls`).MatchString(body) {
		pc.hasList = true
	}
	if regexp.MustCompile(`def update\(cls`).MatchString(body) {
		pc.hasUpdate = true
		if strings.Contains(body, "_from_json(response_raw") && pc.objectTypePut != "" {
			pc.updateReturnsObject = true
		} else {
			pc.updateReturnsID = true
		}
	}
	if regexp.MustCompile(`def delete\(cls`).MatchString(body) {
		pc.hasDelete = true
	}
}

// buildTypeRegistry creates a set of known Go type names.
func buildTypeRegistry(objectClasses, endpointClasses []*pyClass) map[string]bool {
	reg := map[string]bool{}
	for _, c := range objectClasses {
		reg[c.goName] = true
	}
	for _, c := range endpointClasses {
		reg[c.goName] = true
	}
	return reg
}

// resolveTypes replaces unknown pointer types with any.
func resolveTypes(pc *pyClass, registry map[string]bool) {
	resolveFieldTypes(pc.responseFields, registry)
	resolveFieldTypes(pc.requestFields, registry)
}

func resolveFieldTypes(fields []pyField, registry map[string]bool) {
	for i := range fields {
		fields[i].goType = resolveType(fields[i].goType, registry)
	}
}

var primitiveTypes = map[string]bool{
	"string": true, "int": true, "float64": true, "bool": true, "any": true,
}

func resolveType(goType string, registry map[string]bool) string {
	// Handle slices
	if strings.HasPrefix(goType, "[]") {
		inner := resolveType(goType[2:], registry)
		return "[]" + inner
	}

	// Handle pointers
	if strings.HasPrefix(goType, "*") {
		name := goType[1:]
		if primitiveTypes[name] || registry[name] {
			return goType
		}
		return "any"
	}

	return goType
}

// pythonClassToGoName converts a Python class name to a Go type name.
func pythonClassToGoName(name string) string {
	// Strip "ApiObject" suffix
	name = strings.TrimSuffix(name, "ApiObject")
	if name == "" {
		return ""
	}

	// Strip "Object" suffix (for object_.py types like "AmountObject")
	name = strings.TrimSuffix(name, "Object")
	if name == "" {
		return ""
	}

	return name
}

// pythonTypeToGo converts a Python type annotation to a Go type.
func pythonTypeToGo(pyType string, isRequest bool) string {
	pyType = strings.TrimSpace(pyType)
	if pyType == "" {
		return "string" // default to string for unknown
	}

	// Handle list types: list[X] → []X
	if strings.HasPrefix(pyType, "list[") {
		inner := pyType[5 : len(pyType)-1]
		innerGo := pythonTypeToGo(inner, false)
		return "[]" + innerGo
	}

	// Handle object_.TypeName references
	pyType = strings.TrimPrefix(pyType, "object_.")

	// Strip trailing "Object" suffix from references
	pyType = strings.TrimSuffix(pyType, "Object")
	pyType = strings.TrimSuffix(pyType, "ApiObject")

	switch pyType {
	case "str":
		return "string"
	case "int":
		return "int"
	case "float":
		return "float64"
	case "bool":
		if isRequest {
			return "*bool"
		}
		return "bool"
	case "Amount":
		return "*Amount"
	case "Pointer":
		return "*Pointer"
	case "Address":
		return "*Address"
	case "Geolocation":
		return "*Geolocation"
	case "Attachment":
		return "*Attachment"
	}

	// If it starts with uppercase, it's a reference type → pointer
	if len(pyType) > 0 && unicode.IsUpper(rune(pyType[0])) {
		goName := pythonClassToGoName(pyType + "Object") // Try stripping Object
		if goName == "" {
			goName = pyType
		}
		return "*" + goName
	}

	return "string" // fallback
}

// snakeToPascal converts snake_case to PascalCase.
func snakeToPascal(s string) string {
	// Handle trailing underscore (Python reserved word escaping)
	s = strings.TrimSuffix(s, "_")
	s = strings.TrimPrefix(s, "_")

	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		// Handle common abbreviations
		upper := strings.ToUpper(part)
		switch upper {
		case "ID":
			result.WriteString("ID")
		case "URL", "URI":
			result.WriteString(upper)
		case "UUID":
			result.WriteString("UUID")
		case "IP":
			result.WriteString("IP")
		case "API":
			result.WriteString("API")
		case "IBAN":
			result.WriteString("IBAN")
		case "CVC2", "CVC":
			result.WriteString(upper)
		case "ATM":
			result.WriteString("ATM")
		case "PDF":
			result.WriteString("PDF")
		case "PSD2":
			result.WriteString("PSD2")
		case "NFC":
			result.WriteString("NFC")
		case "MAC":
			result.WriteString("MAC")
		case "VAT":
			result.WriteString("VAT")
		case "EAN":
			result.WriteString("EAN")
		case "SDD":
			result.WriteString("SDD")
		case "DBA":
			result.WriteString("DBA")
		case "PAN":
			result.WriteString("PAN")
		case "QR":
			result.WriteString("QR")
		case "SSL":
			result.WriteString("SSL")
		case "OAUTH":
			result.WriteString("OAuth")
		case "TOTP":
			result.WriteString("TOTP")
		case "OTP":
			result.WriteString("OTP")
		default:
			result.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return result.String()
}

// URL analysis helpers

type urlParam struct {
	name   string
	goName string
	goType string
}

// analyzeURL parses a bunq URL pattern and returns the Go format string and parameters.
func analyzeURL(urlPattern string, pc *pyClass) (fmtStr string, params []urlParam) {
	if urlPattern == "" {
		return "", nil
	}

	// First resolve params to know their types
	params = resolveURLParams(urlPattern)

	// Build format string with correct verbs based on param types
	parts := strings.Split(urlPattern, "/")
	var fmtParts []string
	paramIdx := 0
	for _, part := range parts {
		if part == "{}" {
			if paramIdx < len(params) && params[paramIdx].goType == "string" {
				fmtParts = append(fmtParts, "%s")
			} else {
				fmtParts = append(fmtParts, "%d")
			}
			paramIdx++
		} else {
			fmtParts = append(fmtParts, part)
		}
	}

	fmtStr = strings.Join(fmtParts, "/")
	return fmtStr, params
}

func resolveURLParams(urlPattern string) []urlParam {
	parts := strings.Split(urlPattern, "/")

	var params []urlParam
	for i, part := range parts {
		if part != "{}" {
			continue
		}

		// Determine param name from preceding URL segment
		var paramName string
		if i > 0 {
			preceding := parts[i-1]
			paramName = urlSegmentToParamName(preceding)
		}

		goName := snakeToPascal(paramName)
		goType := "int"

		// Special case: attachment-public uses UUID (string)
		if paramName == "attachment_public" {
			goType = "string"
		}

		params = append(params, urlParam{
			name:   paramName,
			goName: goName,
			goType: goType,
		})
	}

	return params
}

func urlSegmentToParamName(segment string) string {
	// Convert URL segment to parameter name
	// "user" → "user", "monetary-account" → "monetary_account"
	return strings.ReplaceAll(segment, "-", "_")
}

// Code generation

func generateObjectsFile(classes []*pyClass, typeRegistry map[string]bool) {
	var b strings.Builder

	b.WriteString("// Code generated by cmd/generate; DO NOT EDIT.\n\n")
	b.WriteString("package bunq\n\n")

	for _, pc := range classes {
		writeStruct(&b, pc, typeRegistry, false)
		b.WriteString("\n")
	}

	if err := os.WriteFile(outputObjectsFile, []byte(b.String()), 0644); err != nil {
		fatal("writing %s: %v", outputObjectsFile, err)
	}
	fmt.Printf("Generated %s\n", outputObjectsFile)
}

func generateEndpointsFile(classes []*pyClass, typeRegistry map[string]bool) {
	var b strings.Builder

	b.WriteString("// Code generated by cmd/generate; DO NOT EDIT.\n\n")
	b.WriteString("package bunq\n\n")

	for _, pc := range classes {
		// Write main response struct
		writeStruct(&b, pc, typeRegistry, false)
		b.WriteString("\n")

		// Write create params if has create method with request fields
		if pc.hasCreate && len(pc.requestFields) > 0 {
			writeParamsStruct(&b, pc, "Create", typeRegistry)
			b.WriteString("\n")
		}

		// Write update params if has update method with request fields
		if pc.hasUpdate && len(pc.requestFields) > 0 {
			writeParamsStruct(&b, pc, "Update", typeRegistry)
			b.WriteString("\n")
		}
	}

	if err := os.WriteFile(outputEndpointsFile, []byte(b.String()), 0644); err != nil {
		fatal("writing %s: %v", outputEndpointsFile, err)
	}
	fmt.Printf("Generated %s\n", outputEndpointsFile)
}

func writeStruct(b *strings.Builder, pc *pyClass, typeRegistry map[string]bool, paramsOnly bool) {
	fields := pc.responseFields
	if paramsOnly {
		fields = pc.requestFields
	}

	if len(fields) == 0 {
		fmt.Fprintf(b, "type %s struct{}\n", pc.goName)
		return
	}

	// Build request field type overrides: when an object is used in both
	// responses and requests (e.g. DraftPaymentEntry), prefer request types
	// since the struct may be embedded in *CreateParams.
	requestTypeOverride := map[string]string{}
	if !paramsOnly {
		for _, rf := range pc.requestFields {
			requestTypeOverride[rf.goName] = rf.goType
		}
	}

	fmt.Fprintf(b, "type %s struct {\n", pc.goName)

	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.goName] {
			continue
		}
		seen[f.goName] = true
		goType := f.goType
		if override, ok := requestTypeOverride[f.goName]; ok && override != goType {
			goType = override
		}
		fmt.Fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", f.goName, goType, f.jsonTag)
	}

	b.WriteString("}\n")
}

func writeParamsStruct(b *strings.Builder, pc *pyClass, action string, typeRegistry map[string]bool) {
	structName := pc.goName + action + "Params"

	if len(pc.requestFields) == 0 {
		fmt.Fprintf(b, "type %s struct{}\n", structName)
		return
	}

	fmt.Fprintf(b, "type %s struct {\n", structName)

	seen := map[string]bool{}
	for _, f := range pc.requestFields {
		if seen[f.goName] {
			continue
		}
		seen[f.goName] = true
		fmt.Fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", f.goName, f.goType, f.jsonTag)
	}

	b.WriteString("}\n")
}

func generateServicesFile(classes []*pyClass) {
	var b strings.Builder

	b.WriteString("// Code generated by cmd/generate; DO NOT EDIT.\n\n")
	b.WriteString("package bunq\n\n")
	b.WriteString("import (\n\t\"context\"\n\t\"fmt\"\n)\n\n")

	// Collect service types with methods
	var serviceClasses []*pyClass
	for _, pc := range classes {
		if pc.hasCreate || pc.hasGet || pc.hasList || pc.hasUpdate || pc.hasDelete {
			serviceClasses = append(serviceClasses, pc)
		}
	}

	// Generate service types
	for _, pc := range serviceClasses {
		serviceName := pc.goName + "Service"
		fmt.Fprintf(&b, "type %s struct{ *service }\n\n", serviceName)

		// Generate methods
		generateServiceMethods(&b, pc)
	}

	// Generate ServiceContainer struct
	b.WriteString("// ServiceContainer holds all generated service accessors.\n")
	b.WriteString("// It is embedded in Client, so you can call e.g. client.Payment.Create(...).\n")
	b.WriteString("type ServiceContainer struct {\n")
	for _, pc := range serviceClasses {
		fmt.Fprintf(&b, "\t%s *%sService\n", pc.goName, pc.goName)
	}
	b.WriteString("}\n\n")

	// Generate initServices method
	b.WriteString("func (c *Client) initServices() {\n")
	b.WriteString("\tc.common.client = c\n")
	for _, pc := range serviceClasses {
		fmt.Fprintf(&b, "\tc.%s = &%sService{&c.common}\n", pc.goName, pc.goName)
	}
	b.WriteString("}\n")

	if err := os.WriteFile(outputServicesFile, []byte(b.String()), 0644); err != nil {
		fatal("writing %s: %v", outputServicesFile, err)
	}
	fmt.Printf("Generated %s\n", outputServicesFile)
}

func generateServiceMethods(b *strings.Builder, pc *pyClass) {
	serviceName := pc.goName + "Service"

	if pc.hasCreate {
		generateCreateMethod(b, pc, serviceName)
	}
	if pc.hasGet {
		generateGetMethod(b, pc, serviceName)
	}
	if pc.hasList {
		generateListMethod(b, pc, serviceName)
	}
	if pc.hasUpdate {
		generateUpdateMethod(b, pc, serviceName)
	}
	if pc.hasDelete {
		generateDeleteMethod(b, pc, serviceName)
	}
}

func generateCreateMethod(b *strings.Builder, pc *pyClass, serviceName string) {
	url := pc.urlCreate
	if url == "" {
		return
	}

	fmtStr, urlParams := analyzeURL(url, pc)
	methodParams := buildMethodParams(urlParams, pc, true)

	// Determine return type
	var returnType, returnParse string
	switch {
	case pc.createReturnsUUID:
		returnType = "string"
		returnParse = "return unmarshalUUID(body)"
	case pc.createReturnsObject:
		key := pc.objectTypePost
		if key == "" {
			key = pc.goName
		}
		returnType = fmt.Sprintf("*%s", pc.goName)
		returnParse = fmt.Sprintf("return unmarshalObject[%s](body, %q)", pc.goName, key)
	default: // returns ID
		returnType = "int"
		returnParse = "return unmarshalID(body)"
	}

	// Method signature
	hasParams := len(pc.requestFields) > 0
	paramsArg := ""
	if hasParams {
		paramsArg = fmt.Sprintf(", params %sCreateParams", pc.goName)
	}

	fmt.Fprintf(b, "func (s *%s) Create(ctx context.Context%s%s) (%s, error) {\n",
		serviceName, methodParams.signature, paramsArg, returnType)

	// Build path
	writePathConstruction(b, fmtStr, urlParams, pc)

	// HTTP call
	if hasParams {
		b.WriteString("\tbody, _, err := s.client.post(ctx, path, params)\n")
	} else {
		b.WriteString("\tbody, _, err := s.client.post(ctx, path, nil)\n")
	}
	writeErrorReturn(b, returnType)
	b.WriteString("\t" + returnParse + "\n")
	b.WriteString("}\n\n")
}

func generateGetMethod(b *strings.Builder, pc *pyClass, serviceName string) {
	url := pc.urlRead
	if url == "" {
		return
	}

	fmtStr, urlParams := analyzeURL(url, pc)
	methodParams := buildMethodParams(urlParams, pc, false)

	key := pc.objectTypeGet
	if key == "" {
		key = pc.goName
	}

	fmt.Fprintf(b, "func (s *%s) Get(ctx context.Context%s) (*%s, error) {\n",
		serviceName, methodParams.signature, pc.goName)

	writePathConstruction(b, fmtStr, urlParams, pc)

	b.WriteString("\tbody, _, err := s.client.get(ctx, path, nil)\n")
	b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fmt.Fprintf(b, "\treturn unmarshalObject[%s](body, %q)\n", pc.goName, key)
	b.WriteString("}\n\n")
}

func generateListMethod(b *strings.Builder, pc *pyClass, serviceName string) {
	url := pc.urlListing
	if url == "" {
		return
	}

	fmtStr, urlParams := analyzeURL(url, pc)
	methodParams := buildMethodParams(urlParams, pc, false)

	key := pc.objectTypeGet
	if key == "" {
		key = pc.goName
	}

	fmt.Fprintf(b, "func (s *%s) List(ctx context.Context%s, opts *ListOptions) (*ListResponse[%s], error) {\n",
		serviceName, methodParams.signature, pc.goName)

	writePathConstruction(b, fmtStr, urlParams, pc)

	b.WriteString("\tbody, _, err := s.client.get(ctx, path, opts.toParams())\n")
	b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fmt.Fprintf(b, "\treturn unmarshalList[%s](body, %q)\n", pc.goName, key)
	b.WriteString("}\n\n")
}

func generateUpdateMethod(b *strings.Builder, pc *pyClass, serviceName string) {
	url := pc.urlUpdate
	if url == "" {
		return
	}

	fmtStr, urlParams := analyzeURL(url, pc)
	methodParams := buildMethodParams(urlParams, pc, false)

	hasParams := len(pc.requestFields) > 0
	paramsArg := ""
	if hasParams {
		paramsArg = fmt.Sprintf(", params %sUpdateParams", pc.goName)
	}

	if pc.updateReturnsObject {
		key := pc.objectTypePut
		if key == "" {
			key = pc.goName
		}
		fmt.Fprintf(b, "func (s *%s) Update(ctx context.Context%s%s) (*%s, error) {\n",
			serviceName, methodParams.signature, paramsArg, pc.goName)

		writePathConstruction(b, fmtStr, urlParams, pc)

		if hasParams {
			b.WriteString("\tbody, _, err := s.client.put(ctx, path, params)\n")
		} else {
			b.WriteString("\tbody, _, err := s.client.put(ctx, path, nil)\n")
		}
		b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
		fmt.Fprintf(b, "\treturn unmarshalObject[%s](body, %q)\n", pc.goName, key)
	} else {
		fmt.Fprintf(b, "func (s *%s) Update(ctx context.Context%s%s) (int, error) {\n",
			serviceName, methodParams.signature, paramsArg)

		writePathConstruction(b, fmtStr, urlParams, pc)

		if hasParams {
			b.WriteString("\tbody, _, err := s.client.put(ctx, path, params)\n")
		} else {
			b.WriteString("\tbody, _, err := s.client.put(ctx, path, nil)\n")
		}
		writeErrorReturn(b, "int")
		b.WriteString("\treturn unmarshalID(body)\n")
	}
	b.WriteString("}\n\n")
}

func generateDeleteMethod(b *strings.Builder, pc *pyClass, serviceName string) {
	url := pc.urlDelete
	if url == "" {
		return
	}

	fmtStr, urlParams := analyzeURL(url, pc)
	methodParams := buildMethodParams(urlParams, pc, false)

	fmt.Fprintf(b, "func (s *%s) Delete(ctx context.Context%s) error {\n",
		serviceName, methodParams.signature)

	writePathConstruction(b, fmtStr, urlParams, pc)

	b.WriteString("\treturn s.client.delete(ctx, path)\n")
	b.WriteString("}\n\n")
}

// resolvedParam holds the resolved Go variable name and whether it's a method parameter
// or derived from the client.
type resolvedParam struct {
	varExpr    string // expression used in fmt.Sprintf args, e.g. "s.client.userID" or "invoiceID"
	paramDecl  string // parameter declaration in method signature, e.g. "invoiceID int" (empty if implicit)
	isImplicit bool   // true for user (always from client)
}

func resolveURLParamNames(urlParams []urlParam) []resolvedParam {
	resolved := make([]resolvedParam, len(urlParams))

	for i, p := range urlParams {
		switch p.name {
		case "user":
			resolved[i] = resolvedParam{
				varExpr:    "s.client.userID",
				isImplicit: true,
			}
		case "monetary_account":
			resolved[i] = resolvedParam{
				varExpr:   "s.client.resolveMonetaryAccountID(monetaryAccountID)",
				paramDecl: "monetaryAccountID int",
			}
		default:
			paramName := toLowerCamelWithID(snakeToPascal(p.name) + "ID")
			resolved[i] = resolvedParam{
				varExpr:   paramName,
				paramDecl: paramName + " " + p.goType,
			}
		}
	}

	return resolved
}

type methodParamsResult struct {
	signature string // e.g. ", monetaryAccountID int, paymentID int"
}

func buildMethodParams(urlParams []urlParam, pc *pyClass, isCreate bool) methodParamsResult {
	resolved := resolveURLParamNames(urlParams)

	var sig strings.Builder
	for _, rp := range resolved {
		if rp.paramDecl != "" {
			sig.WriteString(", ")
			sig.WriteString(rp.paramDecl)
		}
	}

	return methodParamsResult{signature: sig.String()}
}

func writePathConstruction(b *strings.Builder, fmtStr string, urlParams []urlParam, pc *pyClass) {
	if len(urlParams) == 0 {
		fmt.Fprintf(b, "\tpath := %q\n", fmtStr)
		return
	}

	resolved := resolveURLParamNames(urlParams)
	var args []string
	for _, rp := range resolved {
		args = append(args, rp.varExpr)
	}

	fmt.Fprintf(b, "\tpath := fmt.Sprintf(%q, %s)\n", fmtStr, strings.Join(args, ", "))
}

func writeErrorReturn(b *strings.Builder, returnType string) {
	switch returnType {
	case "int":
		b.WriteString("\tif err != nil {\n\t\treturn 0, err\n\t}\n")
	case "string":
		b.WriteString("\tif err != nil {\n\t\treturn \"\", err\n\t}\n")
	default:
		b.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	}
}

func toLowerCamelWithID(s string) string {
	if s == "" {
		return ""
	}

	// Handle ID suffix specially
	if strings.HasSuffix(s, "ID") && len(s) > 2 {
		prefix := s[:len(s)-2]
		return toLowerFirst(prefix) + "ID"
	}

	return toLowerFirst(s)
}

func toLowerFirst(s string) string {
	if s == "" {
		return ""
	}

	// Handle consecutive uppercase (like "URL" → "url")
	runes := []rune(s)
	if len(runes) > 1 && unicode.IsUpper(runes[0]) && unicode.IsUpper(runes[1]) {
		// Find end of uppercase run
		i := 0
		for i < len(runes) && unicode.IsUpper(runes[i]) {
			i++
		}
		if i == len(runes) {
			return strings.ToLower(s)
		}
		// Keep last uppercase char as start of next word
		for j := range i - 1 {
			runes[j] = unicode.ToLower(runes[j])
		}
		return string(runes)
	}

	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// Ensure sort package is used for deterministic output
var _ = sort.Strings
var _ = slices.Contains[[]string]
