FROM node:22.20.0-bookworm-slim
WORKDIR /case
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY main.mjs ./main.mjs
ENTRYPOINT ["node", "main.mjs"]
