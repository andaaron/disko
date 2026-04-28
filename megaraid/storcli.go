package megaraid

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/patrickmn/go-cache"
	"machinerun.io/disko"
)

type storCli struct {
}

// StorCli returns a storcli specific implementation of Query
func StorCli() MegaRaid {
	return &storCli{}
}

type scResultSectionType int

const (
	// UnknownMedia - indicates an unknown media
	rsUnknown scResultSectionType = iota
	rsHeader
	rsVirtDisk
	rsPhysDisks
	rsVirtProps
	rsVdList
	rsDgDriveList
)

const noStorCliRC = 127

type scResultSection struct {
	Type  scResultSectionType
	Name  string
	Lines []string
}

func (sc *storCli) Query(cID int) (Controller, error) {
	// run /cN show          - get PDs and VDs
	// run /cN/vall show all - populate VD Properties and Path
	// run /cN/eall/sall show all - populate Drive.SerialNumber (best-effort)
	var stdout, stderr []byte
	var rc int

	args := []string{fmt.Sprintf("/c%d", cID), "show", "nolog"}

	if stdout, stderr, rc = storcli(args...); rc != 0 {
		var err error = ErrNoStorcli
		if rc != noStorCliRC {
			err = cmdError(args, stdout, stderr, rc)
		}

		return Controller{}, err
	}

	cxDxOut := string(stdout)

	args = []string{fmt.Sprintf("/c%d/vall", cID), "show", "all", "nolog"}

	if stdout, stderr, rc = storcli(args...); rc != 0 {
		return Controller{}, cmdError(args, stdout, stderr, rc)
	}

	cxVxOut := string(stdout)

	// Best-effort: an error here leaves Drive.SerialNumber empty, which
	// disables JBOD matching but does not break VD classification.
	args = []string{fmt.Sprintf("/c%d/eall/sall", cID), "show", "all", "nolog"}
	stdout, _, rc = storcli(args...)
	cxEallSallOut := ""
	if rc == 0 {
		cxEallSallOut = string(stdout)
	}

	return newController(cID, cxDxOut, cxVxOut, cxEallSallOut)
}

func (sc *storCli) DriverSysfsPath() string {
	return SysfsPCIDriversPath
}

func (sc *storCli) GetDiskType(path string, udInfo disko.UdevInfo) (disko.DiskType, error) {
	return disko.Unknown, fmt.Errorf("missing controller to run query")
}

func newController(cID int, cxDxOut, cxVxOut, cxEallSallOut string) (Controller, error) {
	const pathPropName = "OS Drive Name"

	ctrl := Controller{
		ID: cID,
	}

	vds, pds, err := parseCxShow(cxDxOut)
	if err != nil {
		return ctrl, err
	}

	propMap, err := parseVirtProperties(cxVxOut)
	if err == ErrUnsupported {
		propMap = map[int](map[string]string){}
	} else if err != nil {
		return ctrl, err
	}

	ctrl.VirtDrives = vds
	ctrl.Drives = pds
	ctrl.DriveGroups = DriveGroupSet{}

	for vID, vProps := range propMap {
		ctrl.VirtDrives[vID].Properties = vProps
		ctrl.VirtDrives[vID].Path = vProps[pathPropName]
	}

	// Best-effort: ignore parse errors; drives just keep empty SNs.
	if serials, sErr := parseDriveSerials(cxEallSallOut); sErr == nil {
		for _, drive := range pds {
			if sn, ok := serials[driveKey{EID: drive.EID, Slot: drive.Slot}]; ok {
				drive.SerialNumber = sn
			}
		}
	}

	for diskID, drive := range pds {
		dgID := drive.DriveGroup
		if dgID < 0 {
			continue
		}

		dg, ok := ctrl.DriveGroups[dgID]

		if ok {
			dg.Drives[diskID] = drive
		} else {
			ctrl.DriveGroups[dgID] = &DriveGroup{
				ID:     dgID,
				Drives: DriveSet{diskID: drive},
			}
		}
	}

	return ctrl, nil
}

// loadSections - parse a storcli output into sections.
func loadSections(cmdOut string) []scResultSection {
	var header = false
	var curSect scResultSection
	var last string

	equalLine := regexp.MustCompile("^[=]+$")
	rSects := []scResultSection{}

	matchers := []struct {
		stype scResultSectionType
		regex *regexp.Regexp
	}{
		// /c0/v1 :
		{rsVirtDisk, regexp.MustCompile("^/c[0-9]+/v[0-9]+ :$")},
		// PDs for VD 0
		{rsPhysDisks, regexp.MustCompile("^PDs for VD [0-9]+ :$")},
		// PD LIST (storcli /c0 show)
		{rsPhysDisks, regexp.MustCompile("^PD LIST :$")},
		// VD0 Properties (storcli /c0/vall show all)
		{rsVirtProps, regexp.MustCompile("^.*VD[0-9]+ Properties :$")},
		// VD LIST (storcli /c0/dall show all)
		{rsVdList, regexp.MustCompile("^VD LIST :$")},
		// DG DRIVE LIST (storcli /c0/dall show all)
		{rsDgDriveList, regexp.MustCompile("(DG Drive LIST|UN-CONFIGURED DRIVE LIST) :$")},
		{rsUnknown, regexp.MustCompile("^.* :$")},
	}

	for _, cur := range strings.Split(cmdOut, "\n") {
		if !header {
			// header always first.
			header = true
			curSect = scResultSection{
				Type:  rsHeader,
				Name:  "header",
				Lines: []string{},
			}
		} else if equalLine.MatchString(cur) {
			newType := rsUnknown
			for _, m := range matchers {
				if m.regex.MatchString(last) {
					newType = m.stype
					break
				}
			}
			// drop the trailing " :"
			name := last[:len(last)-2]
			rSects = append(rSects, curSect)
			curSect = scResultSection{
				Type:  newType,
				Name:  name,
				Lines: []string{},
			}
			cur = ""
		} else if last != "" {
			curSect.Lines = append(curSect.Lines, last)
		}

		last = cur
	}

	if last != "" {
		curSect.Lines = append(curSect.Lines, last)
	}

	return append(rSects, curSect)
}

func parseKeyValData(lines []string) map[string]string {
	data := map[string]string{}
	const tokNum2 = 2

	for _, line := range lines {
		if line == "" {
			continue
		}

		toks := strings.SplitN(line, " = ", tokNum2)
		if len(toks) != tokNum2 {
			continue
		}

		data[toks[0]] = toks[1]
	}

	return data
}

func filterTableData(lines []string) []string {
	// find the dataLines.  dataLines[0] will be header, all others are rows.
	dashLine := regexp.MustCompile("^-+$")
	dataLines := []string{}

	for i, sepCount := 0, 0; i < len(lines) && sepCount < 3; i++ {
		if lines[i] == "" {
		} else if dashLine.MatchString(lines[i]) {
			sepCount++
		} else {
			dataLines = append(dataLines, lines[i])
		}
	}

	return dataLines
}

func parseTableData(lines []string) []map[string]string {
	// data looks like:
	//   --------------------
	//   field1 field2  field3
	//   --------------------
	//   record record2 record3
	//   ...
	//   --------------------
	const space = ' '

	type colCand struct {
		Left, Right int
	}

	colCands := []*colCand{}
	left := -1
	leadingSpace := true

	// find contiguous sets of whitespace (column Candidates) in the header Line.
	dataLines := filterTableData(lines)

	for i, curChar := range dataLines[0] {
		if curChar == space {
			if left < 0 && !leadingSpace {
				left = i
			}
		} else {
			leadingSpace = false
			if left >= 0 {
				colCands = append(colCands, &colCand{left, i - 1})
				left = -1
			}
		}
	}

	// walk through each columnCandidate range and
	// find the first column where all lines have a space there.
	var column int
	var cuts = []int{}

	for _, colCand := range colCands {
		success := false
		for column = colCand.Left; column < colCand.Right && !success; column++ {
			success = true

			for _, line := range dataLines[1:] {
				if line[column] != space {
					success = false
					break
				}
			}
		}

		cuts = append(cuts, column)
	}

	return cutTableLines(dataLines, cuts)
}

func cutTableLines(dataLines []string, cuts []int) []map[string]string {
	// now cut data lines into pieces at the columns in 'cuts'
	var data = []map[string]string{}
	var headers = []string{}
	var row []rune
	var from, i int

	trim := func(a []rune) string {
		return strings.Trim(string(a), " ")
	}

	row = []rune(dataLines[0])

	for from, i = 0, 0; i < len(cuts); i++ {
		headers = append(headers, trim(row[from:cuts[i]]))
		from = cuts[i]
	}

	headers = append(headers, trim(row[from:]))

	for _, line := range dataLines[1:] {
		rowData := map[string]string{}
		row = []rune(line)
		from = 0

		for i := 0; i < len(cuts); i++ {
			rowData[headers[i]] = trim(row[from:cuts[i]])
			from = cuts[i]
		}

		rowData[headers[len(cuts)]] = trim(row[from:])
		data = append(data, rowData)
	}

	return data
}

func isHeaderNotFound(header map[string]string) bool {
	return header["Status"] == "Failure" &&
		strings.Contains(header["Description"], "not found")
}

func isHeaderUnsupported(header map[string]string) bool {
	return header["Status"] == "Failure" &&
		strings.Contains(header["Description"], "Un-supported command")
}

func getHeaderError(header map[string]string) error {
	if header["Status"] == "Success" {
		return nil
	}

	if isHeaderNotFound(header) {
		return ErrNoController
	} else if isHeaderUnsupported(header) {
		return ErrUnsupported
	}

	if _, err := strconv.Atoi(header["Controller"]); err != nil {
		return fmt.Errorf("storcli controller in header not an int: %s", err)
	}

	return fmt.Errorf("storcli command returned status: %s", header["Status"])
}

// Parse the output of 'storcli /c0 show'
func parseCxShow(cmdOut string) (VirtDriveSet, DriveSet, error) {
	vds := VirtDriveSet{}
	pds := DriveSet{}

	sections := loadSections(cmdOut)

	for _, sect := range sections {
		// Missing Cases: rsDgDriveList, rsUnknown, rsVirtDisk, rsVirtProps
		//exhaustive:ignore
		switch sect.Type {
		case rsHeader:
			if err := getHeaderError(parseKeyValData(sect.Lines)); err != nil {
				return vds, pds, err
			}

		case rsVdList:
			data := parseTableData(sect.Lines)
			for _, vdData := range data {
				vd, err := vdDataToVirtDrive(vdData)
				if err != nil {
					return vds, pds, err
				}

				vds[vd.ID] = &vd
			}
		case rsPhysDisks:
			data := parseTableData(sect.Lines)
			for _, pdData := range data {
				pd, err := pdDataToDrive(pdData)
				if err != nil {
					return vds, pds, err
				}

				pds[pd.ID] = &pd
			}
		}
	}

	return vds, pds, nil
}

// parseVirtProperties - return properties map ("VD0 Properties") by VirtDrive ID
//
//	cmdOut is output of 'storcli /c0/vall show all'
func parseVirtProperties(cmdOut string) (map[int](map[string]string), error) {
	var vID int
	var err error
	const tokNum2 = 2

	nameMatch := regexp.MustCompile("^VD([0-9]+) Properties$")
	vdmap := map[int](map[string]string){}

	sections := loadSections(cmdOut)

	for _, sect := range sections {
		// Missing cases: rsDgDriveList, rsPhysDisks, rsUnknown, rsVdList, rsVirtDisk
		//exhaustive:ignore
		switch sect.Type {
		case rsHeader:
			if err := getHeaderError(parseKeyValData(sect.Lines)); err != nil {
				return vdmap, err
			}
		case rsVirtProps:
			// Extract the VirtDrive Number from the Name (VD0 Properties)
			toks := nameMatch.FindStringSubmatch(sect.Name)

			if len(toks) != tokNum2 {
				return vdmap, fmt.Errorf("failed parsing section '%s'", sect.Name)
			}

			if vID, err = strconv.Atoi(toks[1]); err != nil {
				return vdmap, fmt.Errorf("failed to get int from section '%s'", sect.Name)
			}

			vdmap[vID] = parseKeyValData(sect.Lines)
		}
	}

	return vdmap, nil
}

// driveKey identifies a physical drive by (enclosure, slot) for
// cross-referencing parsed 'storcli /cN' and '/cN/eall/sall' outputs.
type driveKey struct {
	EID  int
	Slot int
}

// parseDriveSerials extracts (EID, Slot) -> SN from the output of
// 'storcli /cN/eall/sall show all'. Returns an empty map (with nil error)
// for empty input, so callers can feed in a best-effort string.
func parseDriveSerials(cmdOut string) (map[driveKey]string, error) {
	out := map[driveKey]string{}

	if strings.TrimSpace(cmdOut) == "" {
		return out, nil
	}

	// Surface Success/Failure/Unsupported via the standard header block.
	for _, sect := range loadSections(cmdOut) {
		if sect.Type == rsHeader {
			if err := getHeaderError(parseKeyValData(sect.Lines)); err != nil {
				return out, err
			}
			break
		}
	}

	// Any line starting with "Drive /cX/eY/sZ " updates the current
	// drive context (covers the summary, state, attributes, etc.
	// sub-sections). The drive's SN appears later in the attributes
	// sub-section.
	driveHdr := regexp.MustCompile(`^Drive /c\d+/e(\d+)/s(\d+)\b`)
	snLine := regexp.MustCompile(`^SN\s*=\s*(.*\S)\s*$`)

	var cur *driveKey

	for _, line := range strings.Split(cmdOut, "\n") {
		if m := driveHdr.FindStringSubmatch(line); m != nil {
			eid, err := strconv.Atoi(m[1])
			if err != nil {
				cur = nil
				continue
			}
			slot, err := strconv.Atoi(m[2])
			if err != nil {
				cur = nil
				continue
			}
			k := driveKey{EID: eid, Slot: slot}
			cur = &k
			continue
		}

		if cur == nil {
			continue
		}

		if m := snLine.FindStringSubmatch(line); m != nil {
			out[*cur] = m[1]
		}
	}

	return out, nil
}

func parseIntOrDash(field string) (int, error) {
	if field == "-" {
		return -1, nil
	}

	return strconv.Atoi(field)
}

// vdDataToVirtDrive - take single data VD row (parseTableData(..)) return a VirtDrive
func vdDataToVirtDrive(data map[string]string) (VirtDrive, error) {
	var dg, vdNum int
	var err error

	nilVd := VirtDrive{}
	dgvd := strings.Split(data["DG/VD"], "/")

	if dg, err = parseIntOrDash(dgvd[0]); err != nil {
		return nilVd, fmt.Errorf("failed to get DriveGroup from %s: %s", dgvd, err)
	}

	if vdNum, err = parseIntOrDash(dgvd[1]); err != nil {
		return nilVd, fmt.Errorf("failed to get VirtDrive Number from %s: %s", dgvd, err)
	}

	return VirtDrive{
		ID:         vdNum,
		DriveGroup: dg,
		Path:       "",
		RaidName:   data["Name"],
		Type:       data["TYPE"],
		Raw:        data,
	}, nil
}

func parseDriveGroupVal(val string) (int, error) {
	known := map[string]int{
		"-": -1, // None
		"F": -2, // Foreign
	}

	if found, ok := known[val]; ok {
		return found, nil
	}

	return parseIntOrDash(val)
}

func pdDataToDrive(data map[string]string) (Drive, error) {
	var err error
	var dID, dg, eID, slot int
	const tokNum2 = 2

	if dID, err = parseIntOrDash(data["DID"]); err != nil {
		return Drive{}, err
	}

	if dg, err = parseDriveGroupVal(data["DG"]); err != nil {
		return Drive{}, err
	}

	toks := strings.SplitN(data["EID:Slt"], ":", tokNum2)
	if len(toks) != tokNum2 {
		return Drive{},
			fmt.Errorf(
				"splitting EID:Slt data '%s' on ':'' returned %d fields, expected 2",
				data["EID:Slt"], len(toks))
	}

	if eID, err = parseIntOrDash(toks[0]); err != nil {
		return Drive{}, err
	}

	if slot, err = parseIntOrDash(toks[1]); err != nil {
		return Drive{}, err
	}

	return Drive{
		ID:         dID,
		EID:        eID,
		Slot:       slot,
		DriveGroup: dg,
		State:      data["State"],
		MediaType:  stringToMediaType(data["Med"]),
		Model:      data["Model"],
		Raw:        data,
	}, nil
}

func stringToMediaType(mtypeStr string) MediaType {
	kmap := map[string]MediaType{
		"UNKNOWN": UnknownMedia,
		"HDD":     HDD,
		"SSD":     SSD,
	}
	if mtype, ok := kmap[mtypeStr]; ok {
		return mtype
	}

	return UnknownMedia
}

func storcli(args ...string) ([]byte, []byte, int) {
	cmd := exec.Command("storcli", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	return stdout.Bytes(), stderr.Bytes(), getCommandErrorRCDefault(err, noStorCliRC)
}

func cmdError(args []string, out []byte, err []byte, rc int) error {
	if rc == 0 {
		return nil
	}

	return fmt.Errorf(
		"command failed [%d]:\n cmd: %v\n out:%s\n err:%s",
		rc, args, out, err)
}

func getCommandErrorRCDefault(err error, rcError int) int {
	if err == nil {
		return 0
	}

	exitError, ok := err.(*exec.ExitError)
	if ok {
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}

	return rcError
}

type cachingStorCli struct {
	mr    MegaRaid
	cache *cache.Cache
}

// CachingStorCli - just a cache for a MegaRaid
func CachingStorCli() MegaRaid {
	const longTime = 5 * time.Minute

	return &cachingStorCli{
		mr:    &storCli{},
		cache: cache.New(longTime, longTime),
	}
}

func (csc *cachingStorCli) Query(cID int) (Controller, error) {
	type qresult struct {
		ctrl Controller
		err  error
	}

	cacheName := fmt.Sprintf("query-%d", cID)
	cached, found := csc.cache.Get(cacheName)

	if found {
		ret := cached.(qresult)
		return ret.ctrl, ret.err
	}

	ctrl, err := csc.mr.Query(cID)
	csc.cache.Set(cacheName, qresult{ctrl: ctrl, err: err}, cache.DefaultExpiration)

	return ctrl, err
}

func (csc *cachingStorCli) GetDiskType(path string, udInfo disko.UdevInfo) (disko.DiskType, error) {
	ctrl, err := csc.Query(0)
	if err != nil {
		if isSoftStorCliErr(err) {
			// Controller tool unavailable or no controller. Fall
			// through with the sentinel so the caller uses udev.
			return disko.Unknown, disko.ErrDiskTypeUndetermined
		}
		return disko.Unknown, err
	}

	for _, vd := range ctrl.VirtDrives {
		if vd.Path == path {
			if ctrl.DriveGroups[vd.DriveGroup].IsSSD() {
				return disko.SSD, nil
			}

			return disko.HDD, nil
		}
	}

	// No VD matched path. Try JBOD/passthrough by matching udev serial
	// against Drive.SerialNumber; fall through with the sentinel on any
	// failure.
	if dType, ok := jbodDiskTypeFromSerial(ctrl, udInfo); ok {
		return dType, nil
	}

	return disko.Unknown, disko.ErrDiskTypeUndetermined
}

// isSoftStorCliErr returns true when err indicates that storcli simply
// cannot answer right now (binary missing, no controller present, or an
// unsupported controller). Callers should fall back to generic detection
// rather than treat these as fatal.
func isSoftStorCliErr(err error) bool {
	return errors.Is(err, ErrNoStorcli) ||
		errors.Is(err, ErrNoController) ||
		errors.Is(err, ErrUnsupported)
}

// jbodDiskTypeFromSerial matches a udev serial against Drive.SerialNumber
// across the controller's Drives. Returns ok=false on missing udev serial,
// no match, collision, or unknown media.
func jbodDiskTypeFromSerial(ctrl Controller, udInfo disko.UdevInfo) (disko.DiskType, bool) {
	serials := udInfo.CollectSerials()
	if len(serials) == 0 {
		return disko.Unknown, false
	}

	var matches []*Drive
	for _, d := range ctrl.Drives {
		if d == nil {
			continue
		}
		sn := strings.TrimSpace(d.SerialNumber)
		if sn == "" {
			continue
		}
		if _, ok := serials[sn]; ok {
			matches = append(matches, d)
		}
	}

	if len(matches) != 1 {
		return disko.Unknown, false
	}

	switch matches[0].MediaType {
	case SSD:
		return disko.SSD, true
	case HDD:
		return disko.HDD, true
	case NVME:
		return disko.NVME, true
	case UnknownMedia:
		return disko.Unknown, false
	}

	return disko.Unknown, false
}

func (csc *cachingStorCli) DriverSysfsPath() string {
	return csc.mr.DriverSysfsPath()
}
