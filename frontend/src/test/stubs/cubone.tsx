// Test stub for @cubone/react-file-manager. Its package `main` points at raw src
// with extensionless imports that don't resolve under vitest's node resolver (the
// browser build uses the `module`/dist entry instead). Nothing we test renders the
// real file manager, so a placeholder keeps the module graph importable.
export function FileManager() {
  return null
}
export default { FileManager }
