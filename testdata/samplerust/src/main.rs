fn main() {
    println!("{}", greet("world"));
}

fn greet(name: &str) -> String {
    format!("hello {}", name)
}
