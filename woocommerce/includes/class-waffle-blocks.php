<?php
defined( 'ABSPATH' ) || exit;

final class Waffle_Blocks extends \Automattic\WooCommerce\Blocks\Payments\Integrations\AbstractPaymentMethodType {
	protected $name = 'waffle';

	public function initialize() {
		$this->settings = Waffle_API::settings();
	}

	public function is_active() {
		$gateways = WC()->payment_gateways()->payment_gateways();
		return isset( $gateways['waffle'] ) && $gateways['waffle']->is_available();
	}

	public function get_payment_method_script_handles() {
		wp_register_script( 'waffle-wc-blocks', plugins_url( 'assets/blocks.js', WAFFLE_WC_FILE ), array( 'wc-blocks-registry', 'wc-settings', 'wp-element', 'wp-html-entities' ), WAFFLE_WC_VERSION, true );
		return array( 'waffle-wc-blocks' );
	}

	public function get_payment_method_data() {
		return array(
			'title'       => $this->get_setting( 'title', 'QRIS & Virtual Account' ),
			'description' => $this->get_setting( 'description', '' ),
			'supports'    => array( 'products' ),
		);
	}
}
