fn add(a: i32, b: i32) -> i32 {
    a + b
}

struct Greeter {
    name: String,
}

impl Greeter {
    fn greet(&self) -> String {
        format!("hi {}", self.name)
    }
}
