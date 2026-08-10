FROM ruby:3.4.7-slim-bookworm
WORKDIR /case
COPY Gemfile ./Gemfile
RUN apt-get update \
    && apt-get install --yes --no-install-recommends build-essential \
    && bundle config set without development \
    && bundle install --jobs 4 \
    && apt-get purge --yes --auto-remove build-essential \
    && rm -rf /var/lib/apt/lists/*
COPY main.rb ./main.rb
ENTRYPOINT ["ruby", "main.rb"]
