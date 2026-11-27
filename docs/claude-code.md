# Setup in Claude Code

1. [Install the executable](../README.md) and find its absolute path.

   ```sh
   command -v bitbucket-cloud-mcp
   ```

2. Store the Atlassian API token in 1Password and copy its secret reference,
   `op://<vault>/<item>/<field>`. Sign in to the
   [1Password CLI](https://developer.1password.com/docs/cli/).

3. Register the server for your user. Replace the binary path with the
   absolute path from step 1. Claude Code saves the `op://` reference in
   `~/.claude.json`.

   ```sh
   claude mcp add-json --scope user bitbucket-cloud '{
     "type": "stdio",
     "command": "op",
     "args": ["run", "--no-masking", "--", "/absolute/path/to/bitbucket-cloud-mcp"],
     "env": {
       "BITBUCKET_WORKSPACE": "<workspace>",
       "BITBUCKET_EMAIL": "<atlassian-email>",
       "BITBUCKET_API_TOKEN": "op://<vault>/<item>/<field>"
     }
   }'
   ```

   `op run` resolves the token reference at launch. Use `--no-masking` to
   preserve the MCP protocol on stdout.

4. Approve 1Password access when the server starts. Check the connection with
   `claude mcp list` and list tools with `/mcp` in a Claude Code session.
