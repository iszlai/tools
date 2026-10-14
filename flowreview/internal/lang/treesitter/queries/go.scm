; Capture roles read by grammar.go: "<role>" wraps the definition node,
; "<role>.name" its identifier, "<role>.receiver" a Go method's receiver type.

(function_declaration
  name: (identifier) @function.name) @function

(method_declaration
  receiver: (parameter_list
    (parameter_declaration
      type: [
        (pointer_type (type_identifier) @method.receiver)
        (type_identifier) @method.receiver
      ]))
  name: (field_identifier) @method.name) @method

(func_literal) @closure

(type_declaration
  (type_spec
    name: (type_identifier) @interface.name
    type: (interface_type))) @interface

(type_declaration
  (type_spec
    name: (type_identifier) @class.name
    type: (struct_type))) @class

(call_expression
  function: (identifier) @call.name) @call

(call_expression
  function: (selector_expression
    field: (field_identifier) @call.name)) @call
