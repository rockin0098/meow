package parser

import (
	"fmt"
	"io/ioutil"
	"strings"
	// . "github.com/rockin0098/flash/base/logger"
)

var classid_output_file = "tl.class.id.go"
var classid_template = `
package mtproto

const (
	TL_LAYER_VERSION = "%v"
)

const (
	TL_CLASS_UNKNOWN int32 = 0
	%v
)

var TL_CLASS_NAME = map[int32]string {
	%v
}

var TL_CLASS_NAME_ID = map[string]int32 {
	%v
}

`

func (t *TLLayer) GenerateTLObjectClassConst() {
	lines := t.Lines
	classIDConst := ""
	className := ""
	classNameID := ""
	for _, line := range lines {

		// 没有id的暂时不解析
		if len(line.ID) == 0 {
			continue
		}

		lineid := int32(convertCRC32(line.ID))
		line.Predicate = strings.Replace(line.Predicate, ".", "_", -1)

		constLine := fmt.Sprintf("TL_CLASS_%v int32 = %d\n", line.Predicate, lineid)
		classIDConst = classIDConst + constLine

		classNameLine := fmt.Sprintf("%d:\"TL_CLASS_%v\",\n", lineid, line.Predicate)
		className = className + classNameLine

		classNameIDLine := fmt.Sprintf("\"TL_CLASS_%v\":%d,\n", line.Predicate, lineid)
		classNameID = classNameID + classNameIDLine
	}

	filecontent := fmt.Sprintf(classid_template, t.Layer, classIDConst, className, classNameID)
	file := t.OutputDir + classid_output_file

	ioutil.WriteFile(file, []byte(filecontent), 0644)
}
