// Keep this the first dependency of every legacy executable. Exit before any
// legacy database, VM lifecycle or listener initialization can run.
process.stderr.write('Virfield v1 is retired. Use the Go v2 virfieldd / virfield-mcp binaries. See docs/v2/README.md.\n');
process.exit(1);
