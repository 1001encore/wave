def greet(name: str) -> str:
    return build_message(name)


def build_message(name: str) -> str:
    return f"Hello, {name}"


def welcome_user(name: str) -> str:
    return greet(name)
