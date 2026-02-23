const uuid GEN_UUID = '00000000-4444-CCCC-ffff-0123456789ab'
const uuid GEN_GUID = '{00112233-4455-6677-8899-aaBBccDDeeFF}'

const i32 HEX = 0x0A

service API {
  async void go(1: byte x, 2: uuid id),
}
