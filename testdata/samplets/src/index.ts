import { helper } from "./lib/util"

export interface Greeter {
  greet(name: string): string
}

export class ConsoleGreeter {
  greet(name: string): string {
    return helper(name)
  }
}

export const makeGreeter = (prefix: string) => {
  return (name: string) => helper(prefix + name)
}
