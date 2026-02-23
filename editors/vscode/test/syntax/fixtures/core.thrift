include "shared.thrift"
namespace go demo.core

/** Service docs */
service DemoService {
  // method comment
  async void ping(1: i32 id) throws (1: string message) (owner = "demo", retries = 2)
}

# hash comment
const bool ENABLED = true
const uuid GEN_UUID = "{00112233-4455-6677-8899-aabbccddeeff}"
