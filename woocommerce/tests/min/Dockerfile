# Oldest supported stack: PHP 7.4, WordPress 6.4, WooCommerce 8.2.
FROM php:7.4-apache
RUN docker-php-ext-install mysqli && a2enmod rewrite
ADD https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar /usr/local/bin/wp
RUN chmod 755 /usr/local/bin/wp && sed -i 's/AllowOverride None/AllowOverride All/' /etc/apache2/apache2.conf && chown www-data:www-data /var/www/html /var/www
USER www-data
RUN php -d memory_limit=512M /usr/local/bin/wp core download --version=6.4.5 --path=/var/www/html
USER root
COPY --chown=www-data:www-data htaccess /var/www/html/.htaccess
