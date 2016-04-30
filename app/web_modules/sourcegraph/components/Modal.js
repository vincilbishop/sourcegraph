import React from "react";

import CSSModules from "react-css-modules";
import styles from "./styles/modal.css";

class Modal extends React.Component {
	constructor(props) {
		super(props);
		this._onClick = this._onClick.bind(this);
		this._handleKeydown = this._handleKeydown.bind(this);
	}

	componentDidMount() {
		if (typeof document !== "undefined") {
			document.addEventListener("keydown", this._handleKeydown);
		}
	}

	componentWillUnmount() {
		if (typeof document !== "undefined") {
			document.removeEventListener("keydown", this._handleKeydown);
		}
	}

	_onClick(e) {
		if (e.target === this.refs.modal_container) {
			if (this.props.onDismiss) this.props.onDismiss();
		}
	}

	_handleKeydown(e: KeyboardEvent) {
		if (e.keyCode === 27 /* ESC */) {
			if (this.props.onDismiss) this.props.onDismiss();
		}
	}

	render() {
		return (
			<div ref="modal_container"
				styleName={this.props.shown ? "container" : "hidden"}
				onClick={this._onClick}>
					{this.props.children}
			</div>
		);
	}
}

Modal.propTypes = {
	shown: React.PropTypes.bool.isRequired,
	onDismiss: React.PropTypes.func,
	children: React.PropTypes.oneOfType([
		React.PropTypes.arrayOf(React.PropTypes.element),
		React.PropTypes.element,
	]),
};

Modal = CSSModules(Modal, styles);
export default Modal;

// dismissModal creates a function that dismisses the modal by setting
// the location state's modal property to null.
export function dismissModal(modalName, location, router) {
	return () => {
		if (location.state && location.state.modal !== modalName) {
			console.error(`location.state.modal is not ${modalName}, is:`, location.state.modal);
		}
		router.replace({...location, state: {...location.state, modal: null}});
	};
}

// LocationStateModal wraps <Modal> and uses a key on the location state
// to determine whether it is displayed. Use LocationStateModal with
// LocationStateToggleLink.
export function LocationStateModal({location, modalName, children, onDismiss}, {router}) {
	const onDismiss2 = () => {
		dismissModal(modalName, location, router)();
		if (onDismiss) onDismiss();
	};
	return (
		<Modal shown={location.state && location.state.modal === modalName}
			onDismiss={onDismiss2}>
			{children}
		</Modal>
	);
}
LocationStateModal.propTypes = {
	location: React.PropTypes.object.isRequired,

	// modalName is the name of the modal (location.state.modal value) that this
	// LocationStateToggleLink component toggles.
	modalName: React.PropTypes.string.isRequired,

	onDismiss: React.PropTypes.func,
};
LocationStateModal.contextTypes = {
	router: React.PropTypes.object.isRequired,
};
