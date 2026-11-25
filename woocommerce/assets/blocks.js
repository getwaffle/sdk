( function () {
	var data = window.wc.wcSettings.getSetting( 'waffle_data', {} );
	var el = window.wp.element.createElement;
	var decode = window.wp.htmlEntities.decodeEntities;
	var label = decode( data.title || 'QRIS & Virtual Account' );
	var Content = function () {
		return el( 'p', null, decode( data.description || '' ) );
	};
	window.wc.wcBlocksRegistry.registerPaymentMethod( {
		name: 'waffle',
		label: el( 'span', null, label ),
		ariaLabel: label,
		content: el( Content ),
		edit: el( Content ),
		canMakePayment: function () {
			return true;
		},
		supports: { features: data.supports || [ 'products' ] },
	} );
} )();
