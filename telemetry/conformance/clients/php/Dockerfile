FROM composer:2.8.12
WORKDIR /case
COPY composer.json ./composer.json
RUN composer install --no-dev --no-interaction --no-progress --classmap-authoritative
COPY main.php ./main.php
ENTRYPOINT ["php", "main.php"]
