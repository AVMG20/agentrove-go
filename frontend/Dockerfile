FROM oven/bun:1-slim

WORKDIR /app

COPY package.json bun.lock ./
RUN bun install --frozen-lockfile

EXPOSE 3000

# Dev server with auto-reload; the compose file mounts the source over /app.
CMD ["bun", "--bun", "run", "dev", "--host", "0.0.0.0", "--port", "3000"]
