; Shared by the typescript and tsx grammars (their node types for
; declarations and calls are identical). Capture roles read by grammar.go.
; "container" marks a node used only to attach an enclosing-class name to
; nested methods, never shown in Outline.

(function_declaration
  name: (identifier) @function.name) @function

(class_declaration
  name: (type_identifier) @class.name) @class

(class_declaration
  name: (type_identifier) @container.name) @container

(abstract_class_declaration
  name: (type_identifier) @class.name) @class

(abstract_class_declaration
  name: (type_identifier) @container.name) @container

(interface_declaration
  name: (type_identifier) @interface.name) @interface

(interface_declaration
  name: (type_identifier) @container.name) @container

(method_definition
  name: (property_identifier) @method.name) @method

(function_expression) @closure

(arrow_function) @closure

(call_expression
  function: (identifier) @call.name) @call

(call_expression
  function: (member_expression
    property: (property_identifier) @call.name)) @call

(new_expression
  constructor: (identifier) @call.name) @call
