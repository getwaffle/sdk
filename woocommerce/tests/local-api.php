<?php
// Test only: point the plugin at the Waffle API on the host.
add_filter( 'waffle_wc_api_base', static function () { return 'http://host.docker.internal:8080'; } );
add_filter( 'waffle_wc_pay_base', static function () { return 'http://localhost:3001'; } );
