#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "tree_sitter/api.h"

TSLanguage *tree_sitter_thrift(void);

typedef struct {
  uint32_t start_byte;
  uint32_t old_end_byte;
  uint32_t new_end_byte;
  uint32_t start_row;
  uint32_t start_col;
  uint32_t old_end_row;
  uint32_t old_end_col;
  uint32_t new_end_row;
  uint32_t new_end_col;
} TwInputEdit;

typedef struct {
  uint32_t start_byte;
  uint32_t end_byte;
  uint32_t start_row;
  uint32_t start_col;
  uint32_t end_row;
  uint32_t end_col;
} TwChangedRange;

typedef struct {
  uint32_t symbol;
  uint32_t start_byte;
  uint32_t end_byte;
  uint32_t child_count;
  uint32_t flags;
} TwNodeInfo;

enum {
  TW_NODE_FLAG_NAMED = 1u << 0,
  TW_NODE_FLAG_ERROR = 1u << 1,
  TW_NODE_FLAG_MISSING = 1u << 2,
  TW_NODE_FLAG_EXTRA = 1u << 3,
  TW_NODE_FLAG_HAS_ERROR = 1u << 4,
};

uintptr_t tw_parser_new(void) {
  return (uintptr_t)ts_parser_new();
}

void tw_parser_delete(uintptr_t parser) {
  ts_parser_delete((TSParser *)parser);
}

uint32_t tw_parser_set_language(uintptr_t parser) {
  return ts_parser_set_language((TSParser *)parser, tree_sitter_thrift()) ? 1 : 0;
}

uintptr_t tw_parser_parse_string(uintptr_t parser, uintptr_t old_tree, const char *src, uint32_t len) {
  return (uintptr_t)ts_parser_parse_string((TSParser *)parser, (TSTree *)old_tree, src, len);
}

void tw_tree_delete(uintptr_t tree) {
  ts_tree_delete((TSTree *)tree);
}

void tw_tree_edit(uintptr_t tree, const TwInputEdit *edit) {
  if (tree == 0 || edit == NULL) {
    return;
  }
  TSInputEdit input_edit = {
      .start_byte = edit->start_byte,
      .old_end_byte = edit->old_end_byte,
      .new_end_byte = edit->new_end_byte,
      .start_point = {.row = edit->start_row, .column = edit->start_col},
      .old_end_point = {.row = edit->old_end_row, .column = edit->old_end_col},
      .new_end_point = {.row = edit->new_end_row, .column = edit->new_end_col},
  };
  ts_tree_edit((TSTree *)tree, &input_edit);
}

uint32_t tw_tree_changed_ranges(uintptr_t old_tree, uintptr_t new_tree, TwChangedRange *out_ranges, uint32_t out_cap) {
  if (old_tree == 0 || new_tree == 0) {
    return 0;
  }

  uint32_t count = 0;
  TSRange *ranges = ts_tree_get_changed_ranges((const TSTree *)old_tree, (const TSTree *)new_tree, &count);
  if (ranges == NULL) {
    return 0;
  }

  uint32_t write_count = count;
  if (write_count > out_cap) {
    write_count = out_cap;
  }
  for (uint32_t i = 0; i < write_count; i++) {
    out_ranges[i].start_byte = ranges[i].start_byte;
    out_ranges[i].end_byte = ranges[i].end_byte;
    out_ranges[i].start_row = ranges[i].start_point.row;
    out_ranges[i].start_col = ranges[i].start_point.column;
    out_ranges[i].end_row = ranges[i].end_point.row;
    out_ranges[i].end_col = ranges[i].end_point.column;
  }

  free(ranges);
  return count;
}

void tw_tree_root_node(uintptr_t tree, TSNode *out_node) {
  *out_node = ts_tree_root_node((TSTree *)tree);
}

static uint32_t tw_node_flags(TSNode node) {
  uint32_t flags = 0;
  if (ts_node_is_named(node)) {
    flags |= TW_NODE_FLAG_NAMED;
  }
  if (ts_node_is_error(node)) {
    flags |= TW_NODE_FLAG_ERROR;
  }
  if (ts_node_is_missing(node)) {
    flags |= TW_NODE_FLAG_MISSING;
  }
  if (ts_node_is_extra(node)) {
    flags |= TW_NODE_FLAG_EXTRA;
  }
  if (ts_node_has_error(node)) {
    flags |= TW_NODE_FLAG_HAS_ERROR;
  }
  return flags;
}

void tw_node_inspect(const TSNode *node, TwNodeInfo *out_info) {
  if (node == NULL || out_info == NULL) {
    return;
  }
  TSNode n = *node;

  out_info->symbol = (uint32_t)ts_node_symbol(n);
  out_info->start_byte = ts_node_start_byte(n);
  out_info->end_byte = ts_node_end_byte(n);
  out_info->child_count = ts_node_child_count(n);
  out_info->flags = tw_node_flags(n);
}

uint32_t tw_node_children(const TSNode *node, TSNode *out_nodes, uint32_t out_cap) {
  if (node == NULL) {
    return 0;
  }

  uint32_t count = ts_node_child_count(*node);
  if (out_nodes == NULL || out_cap == 0) {
    return count;
  }

  uint32_t write_count = count;
  if (write_count > out_cap) {
    write_count = out_cap;
  }
  for (uint32_t i = 0; i < write_count; i++) {
    out_nodes[i] = ts_node_child(*node, i);
  }
  return count;
}

uintptr_t tw_node_type(const TSNode *node) {
  return (uintptr_t)ts_node_type(*node);
}
