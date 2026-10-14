; Python has one node kind for both free functions and class methods;
; grammar.go reclassifies a function_definition as a method when it is
; lexically nested inside a class_definition.

(function_definition
  name: (identifier) @function.name) @function

(class_definition
  name: (identifier) @class.name) @class

(lambda) @closure

(call
  function: (identifier) @call.name) @call

(call
  function: (attribute
    attribute: (identifier) @call.name)) @call
