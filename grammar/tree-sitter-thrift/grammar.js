const PREC = {
  annotation: 1,
};

module.exports = grammar({
  name: 'thrift',

  extras: ($) => [/[\s\uFEFF\u2060]+/, $.comment],

  word: ($) => $.identifier,

  rules: {
    source_file: ($) => repeat($._top_level_declaration),

    comment: () =>
      token(
        choice(
          seq('//', /[^\r\n]*/),
          seq('#', /[^\r\n]*/),
          seq('/*', /[^*]*\*+([^/*][^*]*\*+)*/, '/'),
        ),
      ),

    _top_level_declaration: ($) =>
      choice(
        $.include_declaration,
        $.cpp_include_declaration,
        $.namespace_declaration,
        $.typedef_declaration,
        $.const_declaration,
        $.enum_definition,
        $.senum_definition,
        $.struct_definition,
        $.union_definition,
        $.exception_definition,
        $.service_definition,
      ),

    include_declaration: ($) => seq('include', field('path', $.string_literal), optional($._statement_sep)),

    cpp_include_declaration: ($) => seq('cpp_include', field('path', $.string_literal), optional($._statement_sep)),

    namespace_declaration: ($) =>
      seq(
        'namespace',
        field('scope', $.namespace_scope),
        field('target', $.namespace_target),
        optional($._statement_sep),
      ),

    namespace_scope: () => token(choice('*', /[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*/)),
    namespace_target: () => token(choice('*', /[A-Za-z_][A-Za-z0-9_.-]*/)),

    typedef_declaration: ($) =>
      seq('typedef', field('type', $.type), field('name', $.identifier), optional($.annotations), optional($._statement_sep)),

    const_declaration: ($) =>
      seq(
        'const',
        field('type', $.type),
        field('name', $.identifier),
        '=',
        field('value', $.const_value),
        optional($.annotations),
        optional($._statement_sep),
      ),

    enum_definition: ($) =>
      seq(
        'enum',
        field('name', $.identifier),
        field('body', $.enum_block),
        optional($.annotations),
        optional($._statement_sep),
      ),

    enum_block: ($) => seq('{', repeat($.enum_value), '}'),

    enum_value: ($) =>
      seq(
        field('name', $.identifier),
        optional(seq('=', field('value', $.int_literal))),
        optional($.annotations),
        optional($._statement_sep),
      ),

    senum_definition: ($) =>
      seq(
        'senum',
        field('name', $.identifier),
        '{',
        repeat($.senum_value),
        '}',
        optional($.annotations),
        optional($._statement_sep),
      ),

    senum_value: ($) => seq(field('value', $.string_literal), optional($._statement_sep)),

    struct_definition: ($) => seq('struct', field('name', $.identifier), field('body', $.field_block), optional($.annotations), optional($._statement_sep)),
    union_definition: ($) => seq('union', field('name', $.identifier), field('body', $.field_block), optional($.annotations), optional($._statement_sep)),
    exception_definition: ($) => seq('exception', field('name', $.identifier), field('body', $.field_block), optional($.annotations), optional($._statement_sep)),

    field_block: ($) => seq('{', repeat($.field), '}'),

    field: ($) =>
      seq(
        optional(seq(field('id', $.field_id), ':')),
        optional(field('requiredness', $.requiredness)),
        field('type', $.type),
        field('name', $.identifier),
        repeat($.legacy_field_option),
        optional(seq('=', field('default', $.const_value))),
        optional($.annotations),
        optional($._statement_sep),
      ),

    field_id: ($) => seq(optional(choice('+', '-')), $.int_literal),

    requiredness: () => choice('required', 'optional'),

    legacy_field_option: ($) =>
      choice(
        'xsd_optional',
        'xsd_nillable',
        'xsd_attrs',
        'xsd_all',
        seq('xsd_namespace', $.string_literal),
      ),

    service_definition: ($) =>
      seq(
        'service',
        field('name', $.identifier),
        optional(seq('extends', field('super', $.scoped_identifier))),
        field('body', $.function_block),
        optional($.annotations),
        optional($._statement_sep),
      ),

    function_block: ($) => seq('{', repeat($.function_definition), '}'),

    function_definition: ($) =>
      seq(
        optional(field('modifier', $.function_modifier)),
        field('return_type', $.return_type),
        field('name', $.identifier),
        field('parameters', $.parameter_list),
        optional(field('throws', $.throws_clause)),
        optional($.annotations),
        optional($._statement_sep),
      ),

    function_modifier: () => choice('oneway', 'async'),
    return_type: ($) => choice('void', $.type),

    parameter_list: ($) => seq('(', repeat($.field), ')'),
    throws_clause: ($) => seq('throws', field('parameters', $.parameter_list)),

    type: ($) => choice($.base_type, $.map_type, $.list_type, $.set_type, $.scoped_identifier),

    base_type: () =>
      choice(
        'bool',
        'byte',
        'i8',
        'i16',
        'i32',
        'i64',
        'double',
        'string',
        'binary',
        'uuid',
      ),

    map_type: ($) =>
      seq(
        'map',
        '<',
        field('key', $.type),
        ',',
        field('value', $.type),
        '>',
        optional($.legacy_cpp_type),
      ),

    list_type: ($) => seq('list', '<', field('element', $.type), '>', optional($.legacy_cpp_type)),
    set_type: ($) => seq('set', '<', field('element', $.type), '>', optional($.legacy_cpp_type)),

    legacy_cpp_type: ($) => seq('cpp_type', $.string_literal),

    annotations: ($) => prec.right(PREC.annotation, seq('(', commaSep1($.annotation), optional(','), ')')),

    annotation: ($) => seq(field('name', $.identifier), optional(seq('=', field('value', $.annotation_value)))),
    annotation_value: ($) =>
      choice($.uuid_literal, $.string_literal, $.int_literal, $.float_literal, $.bool_literal, $.identifier),

    const_value: ($) =>
      choice(
        $.int_literal,
        $.float_literal,
        $.uuid_literal,
        $.string_literal,
        $.bool_literal,
        $.const_list,
        $.const_map,
        $.scoped_identifier,
      ),

    const_list: ($) => seq('[', commaSep($.const_value), optional(','), ']'),
    const_map: ($) => seq('{', commaSep($.const_map_entry), optional(','), '}'),
    const_map_entry: ($) => seq(field('key', $.const_value), choice(':', '='), field('value', $.const_value)),

    bool_literal: () => choice('true', 'false'),
    int_literal: () => token(choice(/0[xX][0-9A-Fa-f]+/, /[0-9]+/)),
    float_literal: () =>
      token(
        choice(
          /[0-9]+\.[0-9]+([eE][+-]?[0-9]+)?/,
          /[0-9]+[eE][+-]?[0-9]+/,
          /\.[0-9]+([eE][+-]?[0-9]+)?/,
        ),
      ),

    string_literal: () =>
      token(
        choice(
          seq('"', repeat(choice(/[^"\\\r\n]+/, /\\./)), '"'),
          seq("'", repeat(choice(/[^'\\\r\n]+/, /\\./)), "'"),
        ),
      ),
    uuid_literal: () =>
      token(
        choice(
          /"[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}"/,
          /'[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}'/,
          /"\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}"/,
          /'\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}'/,
        ),
      ),

    identifier: () => /[A-Za-z_][A-Za-z0-9_]*/,
    scoped_identifier: ($) => seq($.identifier, repeat(seq('.', $.identifier))),

    _statement_sep: () => choice(',', ';'),
  },
});

function commaSep(rule) {
  return optional(commaSep1(rule));
}

function commaSep1(rule) {
  return seq(rule, repeat(seq(',', rule)));
}
