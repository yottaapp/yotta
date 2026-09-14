/* Generated from Diagnostic Go types. Do not edit. */

export interface YottaCompilerDiagnostic {
  code: string
  fieldPath?: string[]
  fix?: DiagnosticFix
  graphPath?: string[]
  message?: string
  nodeId?: string
  params?: {
    [k: string]: any
  }
  severity: 'error' | 'warning' | 'info'
}
export interface DiagnosticFix {
  fieldPath: string[]
  graphPath?: string[]
  kind: 'set_field' | 'remove_field'
  nodeId?: string
  value?: any
}
