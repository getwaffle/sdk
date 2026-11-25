<?php
/**
 * Plugin Name: Waffle for WooCommerce
 * Plugin URI: https://getwaffle.id
 * Description: Terima QRIS dan virtual account di WooCommerce lewat halaman bayar Waffle.
 * Version: 0.1.0
 * Requires at least: 6.4
 * Requires PHP: 7.4
 * Requires Plugins: woocommerce
 * WC requires at least: 8.2
 * Author: PT Arsaloka Inovasi Digital
 * License: GPL-2.0-or-later
 * Text Domain: waffle-woocommerce
 */

defined( 'ABSPATH' ) || exit;

define( 'WAFFLE_WC_VERSION', '0.1.0' );
define( 'WAFFLE_WC_FILE', __FILE__ );

add_action(
	'before_woocommerce_init',
	static function () {
		if ( class_exists( \Automattic\WooCommerce\Utilities\FeaturesUtil::class ) ) {
			\Automattic\WooCommerce\Utilities\FeaturesUtil::declare_compatibility( 'custom_order_tables', __FILE__, true );
			\Automattic\WooCommerce\Utilities\FeaturesUtil::declare_compatibility( 'cart_checkout_blocks', __FILE__, true );
		}
	}
);

add_action(
	'plugins_loaded',
	static function () {
		if ( ! class_exists( 'WC_Payment_Gateway' ) ) {
			return;
		}
		require_once __DIR__ . '/includes/class-waffle-api.php';
		require_once __DIR__ . '/includes/class-waffle-orders.php';
		require_once __DIR__ . '/includes/class-waffle-webhook.php';
		require_once __DIR__ . '/includes/class-waffle-gateway.php';

		add_filter(
			'woocommerce_payment_gateways',
			static function ( $gateways ) {
				$gateways[] = 'Waffle_Gateway';
				return $gateways;
			}
		);
		add_action( 'rest_api_init', array( 'Waffle_Webhook', 'register' ) );
		add_action( 'waffle_wc_check_order', array( 'Waffle_Orders', 'check' ) );
		add_action( 'woocommerce_thankyou_waffle', array( 'Waffle_Orders', 'check' ), 5 );
	}
);

add_action(
	'woocommerce_blocks_payment_method_type_registration',
	static function ( $registry ) {
		require_once __DIR__ . '/includes/class-waffle-blocks.php';
		$registry->register( new Waffle_Blocks() );
	}
);

add_filter(
	'plugin_action_links_' . plugin_basename( __FILE__ ),
	static function ( $links ) {
		$url = admin_url( 'admin.php?page=wc-settings&tab=checkout&section=waffle' );
		array_unshift( $links, '<a href="' . esc_url( $url ) . '">' . esc_html__( 'Pengaturan', 'waffle-woocommerce' ) . '</a>' );
		return $links;
	}
);
