(comment) @comment
(string_literal) @string
(uuid_literal) @string.special
(int_literal) @number
(float_literal) @number.float
(bool_literal) @constant.builtin.boolean
(base_type) @type.builtin

[
  "include"
  "cpp_include"
  "namespace"
  "typedef"
  "const"
  "enum"
  "senum"
  "struct"
  "union"
  "exception"
  "service"
  "extends"
  "oneway"
  "async"
  "throws"
  "required"
  "optional"
  "void"
  "map"
  "list"
  "set"
  "cpp_type"
] @keyword

[
  ","
  ";"
  ":"
  "="
  "."
] @punctuation.delimiter

[
  "{"
  "}"
  "("
  ")"
  "["
  "]"
  "<"
  ">"
] @punctuation.bracket

(struct_definition name: (identifier) @type.definition)
(union_definition name: (identifier) @type.definition)
(exception_definition name: (identifier) @type.definition)
(enum_definition name: (identifier) @type.definition)
(senum_definition name: (identifier) @type.definition)
(service_definition name: (identifier) @type.definition)
(typedef_declaration name: (identifier) @type.definition)
(const_declaration name: (identifier) @constant)
(field name: (identifier) @property)
(function_definition name: (identifier) @function)
(annotation name: (identifier) @attribute)
