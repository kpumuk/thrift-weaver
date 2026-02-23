include "a.thrift";
cpp_include "b.h"

namespace go foo.bar
namespace rb foo.bar

typedef i32 ID
typedef map<string, list<i64>> NameIndex

struct Example {
  1: required ID id,
}
