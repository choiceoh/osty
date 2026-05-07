### 10.13 UUID (`std.uuid`)

```osty
use std.uuid

let id = uuid.v4()                                   // random
let sortable = uuid.v7()                             // time-ordered

let text = id.toString()
let parsed: Uuid = uuid.parse(text)?
```

API:

```
uuid.v4() -> Uuid
uuid.v7() -> Uuid                         // preferred for database keys
uuid.parse(text: String) -> Result<Uuid, Error>
uuid.nil() -> Uuid

Uuid.toString(self) -> String
Uuid.toBytes(self) -> Bytes               // 16 bytes
```

`Uuid` implements `Equal`, `Hashable`, `Ordered`.
