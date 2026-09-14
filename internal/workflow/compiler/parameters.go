package compiler

import (
	"encoding/json"
	"fmt"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"strconv"

	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func ValidateParameterValues(variables []schema.Variable, values map[string]json.RawMessage, catalog nodecatalog.Snapshot) []Diagnostic {
	resolved, diagnostics := resolveParameterValues(variables, values)
	_, typeDiagnostics := compileStateVariables(resolved, catalog)
	annotateParameterDiagnostics(variables, typeDiagnostics)
	return append(diagnostics, typeDiagnostics...)
}

func annotateParameterDiagnostics(variables []schema.Variable, diagnostics []Diagnostic) {
	for index := range diagnostics {
		d := &diagnostics[index]
		if len(d.FieldPath) < 2 || d.FieldPath[0] != "variables" {
			continue
		}
		variableIndex, err := strconv.Atoi(d.FieldPath[1])
		if err != nil || variableIndex < 0 || variableIndex >= len(variables) {
			continue
		}
		parameter := variables[variableIndex].Parameter
		if parameter == nil {
			continue
		}
		d.Params["parameterId"] = parameter.ID
		d.Params["parameterLabel"] = parameter.Label
	}
}

// resolveParameterValues owns the only overlay from local configuration onto
// executable initial state. It never mutates Source, its hash or its defaults.
// Unknown IDs belong to removed declarations and intentionally have no effect.
func resolveParameterValues(variables []schema.Variable, values map[string]json.RawMessage) ([]schema.Variable, []Diagnostic) {
	result := append([]schema.Variable(nil), variables...)
	var diagnostics []Diagnostic
	for index, variable := range variables {
		if variable.Parameter == nil {
			continue
		}
		value, exists := values[variable.Parameter.ID]
		if !exists {
			value = variable.Default
		}
		if err := variable.Parameter.ValidateValue(value); err != nil {
			d := diagnostic(CodeInvalidStateVariable, []string{"variables", fmt.Sprint(index), "parameter"}, "")
			d.Params["reason"] = err.Error()
			d.Params["parameterId"] = variable.Parameter.ID
			d.Params["parameterLabel"] = variable.Parameter.Label
			diagnostics = append(diagnostics, d)
			continue
		}
		result[index].Default = append(json.RawMessage(nil), value...)
	}
	return result, diagnostics
}
