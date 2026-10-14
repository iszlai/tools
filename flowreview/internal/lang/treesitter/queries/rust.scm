; Rust has one node kind for both free functions and impl methods;
; grammar.go reclassifies a function_item as a method when it is lexically
; nested inside an impl_item's "container" capture (the "container" role is
; never shown in Outline, only used to find the enclosing type name).

(function_item
  name: (identifier) @function.name) @function

(impl_item
  type: (type_identifier) @container.name) @container

(struct_item
  name: (type_identifier) @class.name) @class

(trait_item
  name: (type_identifier) @interface.name) @interface

(closure_expression) @closure

(call_expression
  function: (identifier) @call.name) @call

(call_expression
  function: (field_expression
    field: (field_identifier) @call.name)) @call
