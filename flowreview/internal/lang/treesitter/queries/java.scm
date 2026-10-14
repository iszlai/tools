; Capture roles read by grammar.go. "container" marks a node used only to
; attach an enclosing-class name to nested methods, never shown in Outline.

(class_declaration
  name: (identifier) @class.name) @class

(class_declaration
  name: (identifier) @container.name) @container

(interface_declaration
  name: (identifier) @interface.name) @interface

(interface_declaration
  name: (identifier) @container.name) @container

(enum_declaration
  name: (identifier) @container.name) @container

(record_declaration
  name: (identifier) @container.name) @container

(method_declaration
  name: (identifier) @method.name) @method

(constructor_declaration
  name: (identifier) @constructor.name) @constructor

(lambda_expression) @closure

(method_invocation
  name: (identifier) @call.name) @call

(object_creation_expression
  type: (type_identifier) @call.name) @call
