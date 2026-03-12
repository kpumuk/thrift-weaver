include "types.thrift"

service ExampleService extends types.ParentService {
  types.User ping(1: types.User input) throws (1: types.UserError err)
}
